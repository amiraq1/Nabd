package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"nabd/internal/provider"
)

// Sink receives every event. The journal is one; the UI is another.
// Emit must not block for long: the loop waits on it.
type Sink interface {
	Emit(Event) error
}

// Tools runs a tool call. Empty at v0.2 -- the loop is written against
// the interface now so that v0.4 adds tools without touching this file.
type Tools interface {
	Specs() []provider.ToolSpec
	Run(ctx context.Context, c provider.ToolCall) (out string, ok bool, err error)
}

// Loop turns one user message into a settled conversation: it streams a
// turn, runs whatever tools the model asked for, and streams again, until
// the model stops asking. Every observable step becomes an Event.
type Loop struct {
	Provider provider.Provider
	Tools    Tools
	Sink     Sink
	System   string
	MaxTurns int
	Gate     Gate
	Human    Asker
	Budget   *Budget
	warned   bool

	mu     sync.Mutex
	seq    int
	parent int
	hist   []Event
}

// ErrMaxTurns means the model kept calling tools past the ceiling. It is
// a bug guard, not a normal ending: a loop that never settles is a loop.
var ErrMaxTurns = errors.New("turn ceiling reached")

func (l *Loop) Start(banner string) error {
	return l.emit(Event{Type: RunStart, Text: banner})
}

// Run handles one user message to completion. Cancel ctx to interrupt;
// the partial text already emitted stays in the journal, followed by an
// Interrupted event, because pretending it was never said is a lie.
func (l *Loop) Run(ctx context.Context, userText string) error {
	if err := l.emit(Event{Type: UserMsg, Text: userText}); err != nil {
		return err
	}

	maxTurns := l.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 12
	}

	for turn := 0; turn < maxTurns; turn++ {
		ms := Squeeze(Messages(Live(l.hist)), KeepFullRounds)
		if p := l.Budget.Pressure(ms); p > 0.75 {
			if err := l.Compact(ctx, l.Budget.Usable()*4/10); err != nil {
				l.emit(Event{Type: Notice, Text: "تعذّر الضغط: " + err.Error()})
			} else {
				ms = Squeeze(Messages(Live(l.hist)), KeepFullRounds)
				l.emit(Event{Type: Notice, Text: fmt.Sprintf("ضُغط السياق · %d%% ← %d%%",
					int(p*100), int(l.Budget.Pressure(ms)*100))})
				l.warned = false
			}
		} else if p > 0.6 && !l.warned {
			l.warned = true
			l.emit(Event{Type: Notice, Text: fmt.Sprintf("السياق %d%%", int(p*100))})
		}

		calls, stop, err := l.streamTurn(ctx, ms)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				_ = l.emit(Event{Type: Interrupted, Text: "ctrl+c"})
				return nil // an interruption is an outcome, not a failure
			}
			_ = l.emit(Event{Type: RunError, Err: err.Error()})
			return err
		}
		if len(calls) == 0 {
			if stop == "max_tokens" {
				_ = l.emit(Event{Type: Notice, Text: "بلغ حدّ الطول · اطلب المتابعة"})
			}
			return nil
		}

		_, interrupted, err := l.runCalls(ctx, calls)
		// Results are appended even when interrupted: the API rejects an
		// assistant tool_use with no matching tool_result on the next turn.
		if err != nil {
			_ = l.emit(Event{Type: RunError, Err: err.Error()})
			return err
		}
		if interrupted {
			_ = l.emit(Event{Type: Interrupted, Text: "ctrl+c"})
			return nil
		}
	}

	_ = l.emit(Event{Type: RunError, Err: ErrMaxTurns.Error()})
	return ErrMaxTurns
}

// streamTurn consumes exactly one assistant turn.
func (l *Loop) streamTurn(ctx context.Context, ms []provider.Message) ([]provider.ToolCall, string, error) {
	if err := l.emit(Event{Type: TurnStart}); err != nil {
		return nil, "", err
	}

	var specs []provider.ToolSpec
	if l.Tools != nil {
		specs = l.Tools.Specs()
	}

	ch, err := l.Provider.Stream(ctx, provider.Request{
		System:   l.System,
		Messages: ms,
		Tools:    specs,
	})
	if err != nil {
		return nil, "", err
	}

	var (
		text  string
		calls []provider.ToolCall
		stop  string
	)
	for c := range ch {
		switch c.Kind {
		case provider.ChunkText:
			text += c.Text
			if err := l.emit(Event{Type: TextDelta, Text: c.Text}); err != nil {
				return nil, "", err
			}

		case provider.ChunkToolCall:
			calls = append(calls, *c.Call)

		case provider.ChunkStop:
			stop = c.Stop

		case provider.ChunkError:
			// Drain so the provider goroutine is never left blocked.
			for range ch {
			}
			return nil, "", c.Err
		}
	}

	// Record the assistant turn even if it was pure tool calls: the next
	// request must contain the tool_use blocks it is answering.
	if text != "" || len(calls) > 0 {
	}
	if err := l.emit(Event{Type: TurnEnd}); err != nil {
		return nil, "", err
	}
	return calls, stop, nil
}

// runCalls executes the batch in order. Order matters: the model asked
// for read-then-write for a reason, and parallelism would gain a phone
// nothing but a race.
func (l *Loop) runCalls(ctx context.Context, calls []provider.ToolCall) ([]provider.ToolResult, bool, error) {
	results := make([]provider.ToolResult, 0, len(calls))

	for i, c := range calls {
		if ctx.Err() != nil {
			// Every remaining call still needs a result block.
			for _, rest := range calls[i:] {
				results = append(results, provider.ToolResult{
					ID: rest.ID, Output: "أُلغي", IsErr: true,
				})
			}
			return results, true, nil
		}

		ac := ToolCall{ID: c.ID, Name: c.Name, Args: c.Input}
		d, why := l.decide(ctx, ac, l.emit)
		if d == Deny {
			msg := "رُفض تنفيذ " + c.Name
			if why != "" {
				msg += ": " + why
			}
			ac.OK, ac.Output = false, msg
			l.emit(Event{Type: ToolEnd, Call: &ac})
			results = append(results, provider.ToolResult{ID: c.ID, Output: msg, IsErr: true})
			continue
		}

		if err := l.emit(Event{Type: ToolStart, Call: &ac}); err != nil {
			return results, false, err
		}

		start := time.Now()
		out, err := l.exec(ctx, c)
		if err != nil {
			out.Text, out.OK = err.Error(), false
		}
		ms := time.Since(start).Milliseconds()

		results = append(results, provider.ToolResult{ID: c.ID, Output: out.Text, IsErr: !out.OK})
		if eerr := l.emit(Event{Type: ToolEnd, Call: &ToolCall{
			ID: c.ID, Name: c.Name, Output: out.Text, OK: out.OK,
			Exit: out.Exit, Signal: out.Signal, MS: ms,
		}}); eerr != nil {
			return results, false, eerr
		}

		// A mutation leaves a persisted fingerprint behind. The loop is the
		// only writer of Seq/Parent, so the EditRecord event is emitted here
		// (not from inside tools, which must not reach into the journal).
		if out.OK && (c.Name == "write_file" || c.Name == "edit_file") {
			if er, ok := l.Tools.(interface{ LastEdit() *EditRecord }); ok {
				if rec := er.LastEdit(); rec != nil {
					if eerr := l.emit(Event{Type: EventEdit, Edit: rec}); eerr != nil {
						return results, false, eerr
					}
				}
			}
		}
	}
	return results, false, nil
}

// exec isolates the tool from the loop: a panicking tool must not take
// the conversation down with it.
func (l *Loop) exec(ctx context.Context, c provider.ToolCall) (out Outcome, err error) {
	if l.Tools == nil {
		return Outcome{OK: false}, fmt.Errorf("لا أدوات في هذه النسخة: %s", c.Name)
	}
	defer func() {
		if r := recover(); r != nil {
			out = Outcome{OK: false}
			err = fmt.Errorf("panic في %s: %v", c.Name, r)
		}
	}()
	if d, ok := l.Tools.(interface {
		RunDetailed(context.Context, string, []byte) (Outcome, error)
	}); ok {
		return d.RunDetailed(ctx, c.Name, c.Input)
	}
	txt, good, e := l.Tools.Run(ctx, c)
	return Outcome{Text: txt, OK: good}, e
}

// emit stamps identity and hands the event on. Seq and Parent are
// assigned here and nowhere else, which is what makes the journal a
// tree rather than a pile.
func (l *Loop) emit(e Event) error {
	l.emitAt(l.parent, e)
	return nil
}

// Note lets a human action enter the journal through the same door events
// use, so seq and parent stay the single tree they were designed to be.
func (l *Loop) Note(text string) {
	l.emit(Event{Type: Notice, Text: text})
}

// Fanout sends each event to several sinks, stopping at the first error:
// if the journal cannot be written, the UI should not pretend otherwise.
type Fanout []Sink

func (f Fanout) Emit(e Event) error {
	for _, s := range f {
		if s == nil {
			continue
		}
		if err := s.Emit(e); err != nil {
			return err
		}
	}
	return nil
}

// Seed adopts a previous branch as this run's history. The new journal
// starts empty on purpose: sessions stay separate files, the tree lives in
// memory. Merging files is a v0.8 problem, not a v0.7 one.
func (l *Loop) Seed(evs []Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hist = append(l.hist, evs...)
	if n := len(evs); n > 0 {
		l.seq = evs[n-1].Seq
		l.parent = evs[n-1].Seq
	}
}

func (l *Loop) Hist() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, len(l.hist))
	copy(out, l.hist)
	return out
}
