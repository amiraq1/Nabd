package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
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
type MessageEstimator func([]provider.Message) int

type Loop struct {
	Provider         provider.Provider
	Tools            Tools
	Sink             Sink
	System           string
	MaxTurns         int
	Gate             Gate
	Human            Asker
	Budget           *Budget
	EstimateMessages MessageEstimator
	CompactBudget    int
	KeepFullRounds   int
	// RateLimitBudget is the max 429 events allowed per Run(); 0 means 3.
	RateLimitBudget int
	// now is the clock the loop uses for rate-limit timing. nil means the
	// real wall clock; tests inject a fake so they can prove escalation
	// without sleeping.
	now    func() time.Time
	warned bool
	ended  bool // true once End() has been called; guards against double RunEnd

	mu     sync.Mutex
	seq    int
	parent int
	hist   []Event
	// rateLimitState tracks consecutive 429s for the active Run().
	// It is reset at the start of each Run() and after every successful turn.
	rateLimitHits      int           // consecutive 429s since last success
	lastRateLimitTime  time.Time     // when the most recent 429 landed (for cooldown)
	providerRetryAfter time.Duration // provider-declared wait for the most recent 429
	rateLimitTotalWait time.Duration // cumulative wait spent on 429s this Run
	rateLimitAttempts  int           // turns that ended in 429 since last success
}

func (l *Loop) clockNow() time.Time {
	if l.now == nil {
		return time.Now()
	}
	return l.now()
}

func (l *Loop) estimateMessages(ms []provider.Message) int {
	if l.EstimateMessages != nil {
		return l.EstimateMessages(ms)
	}
	return EstimateMessages(ms)
}

func (l *Loop) keepFullRounds() int {
	if l.KeepFullRounds > 0 {
		return l.KeepFullRounds
	}
	return KeepFullRounds
}

func (l *Loop) compactTarget() int {
	if l.CompactBudget > 0 {
		return l.CompactBudget
	}
	return l.Budget.Usable() * 4 / 10
}

func (l *Loop) pressure(ms []provider.Message) float64 {
	usable := l.Budget.Usable()
	if usable <= 0 {
		return 1
	}
	estimated := int(float64(l.estimateMessages(ms)) * l.Budget.Ratio())
	return float64(estimated) / float64(usable)
}

// ErrMaxTurns means the model kept calling tools past the ceiling. It is
// a bug guard, not a normal ending: a loop that never settles is a loop.
var ErrMaxTurns = errors.New("turn ceiling reached")

// ErrRateLimitBudget means too many 429s arrived in a single Run(). The
// session is intact; the caller should wait before retrying.
var ErrRateLimitBudget = errors.New("rate limit budget exhausted")

// errTurnRateLimited is returned by streamTurn when the provider responded
// with a 429. It signals Run() to wait and retry this turn rather than
// counting it as a success (which would reset the consecutive-429 counter).
var errTurnRateLimited = errors.New("turn ended in 429, retry after waiting")

func (l *Loop) rateLimitCeiling() int {
	if l.RateLimitBudget > 0 {
		return l.RateLimitBudget
	}
	return 3
}

// rateLimitWait returns how long to wait before the next attempt after a
// 429. Policy (single source of truth):
//   - If the provider declared a Retry-After, use ceil(it) to whole seconds.
//   - Otherwise use exponential backoff: 1, 2, 4, 8, then capped at 10s.
//   - The result is capped at maxSingleWait (120s).
//
// This replaces the old hits×3s cooldown, which double-waited on top of the
// provider's own wait.
func rateLimitWait(retryAfter time.Duration, consecutiveHits int) time.Duration {
	const maxSingleWait = 120 * time.Second
	var wait time.Duration
	if retryAfter > 0 {
		// Round UP to whole seconds, matching what the provider actually waits.
		wait = time.Duration(math.Ceil(retryAfter.Seconds())) * time.Second
	} else {
		// Exponential backoff: 1, 2, 4, 8, 10, 10... seconds.
		wait = time.Duration(1<<(consecutiveHits-1)) * time.Second
		if wait > 10*time.Second {
			wait = 10 * time.Second
		}
	}
	if wait > maxSingleWait {
		wait = maxSingleWait
	}
	return wait
}

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

	// Reset rate-limit counters so each Run() gets its own budget.
	l.mu.Lock()
	l.rateLimitHits = 0
	l.lastRateLimitTime = time.Time{}
	l.providerRetryAfter = 0
	l.rateLimitTotalWait = 0
	l.rateLimitAttempts = 0
	l.mu.Unlock()

	maxTurns := l.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 12
	}

	// Absolute rate-limit termination bounds, independent of the hits
	// ceiling: a provider that keeps answering 429 without a usable
	// retry_after must not be able to stall the loop forever. Whichever
	// bound is reached first aborts the run.
	const (
		maxRateLimitWait    = 120 * time.Second
		maxRateLimitAttempt = 8
	)
	for turn := 0; turn < maxTurns; turn++ {
		// CHECK A: circuit breakers before doing any more work.
		l.mu.Lock()
		hits := l.rateLimitHits
		last := l.lastRateLimitTime
		retryAfter := l.providerRetryAfter
		totalWait := l.rateLimitTotalWait
		attempts := l.rateLimitAttempts
		l.mu.Unlock()

		// Absolute guard: total time already spent waiting on 429s, or the
		// number of consecutive 429-aborted turns, must cap the run even if
		// the hits ceiling is configured higher.
		if totalWait >= maxRateLimitWait || attempts >= maxRateLimitAttempt {
			_ = l.emit(Event{Type: Notice, Text: fmt.Sprintf(
				"rate limit absolute bound reached (%s waited, %d attempts) · wait and retry",
				totalWait.Round(time.Second), attempts)})
			_ = l.emit(Event{Type: RunError, Err: ErrRateLimitBudget.Error()})
			return ErrRateLimitBudget
		}

		if hits >= l.rateLimitCeiling() {
			_ = l.emit(Event{Type: Notice, Text: fmt.Sprintf(
				"rate limit budget exhausted (%d/429s in this run) · wait and retry", hits)})
			_ = l.emit(Event{Type: RunError, Err: ErrRateLimitBudget.Error()})
			return ErrRateLimitBudget
		}

		// Wait before retrying after a 429. The agent is the sole owner of
		// the wait; the provider only reports. Policy: ceil(retry_after) if
		// the provider declared one, else exponential backoff (1,2,4,8,10s),
		// capped at 120s. This replaces the old hits×3s cooldown which
		// double-waited on top of the provider's own wait.
		if hits > 0 && !last.IsZero() {
			wait := rateLimitWait(retryAfter, hits)
			if elapsed := l.clockNow().Sub(last); elapsed < wait {
				remaining := wait - elapsed
				select {
				case <-ctx.Done():
					_ = l.emit(Event{Type: Interrupted, Text: "ctrl+c"})
					return nil
				case <-time.After(remaining):
				}
			}
		}

		ms := Squeeze(Messages(Live(l.hist)), l.keepFullRounds())
		if p := l.pressure(ms); p > 0.75 {
			if err := l.Compact(ctx, l.compactTarget()); err != nil {
				l.emit(Event{Type: Notice, Text: "compact failed: " + err.Error()})
			} else {
				ms = Squeeze(Messages(Live(l.hist)), l.keepFullRounds())
				l.emit(Event{Type: Notice, Text: fmt.Sprintf("context compacted · %d%% → %d%%",
					int(p*100), int(l.Budget.Pressure(ms)*100))})
				l.warned = false
			}
		} else if p > 0.6 && !l.warned {
			l.warned = true
			l.emit(Event{Type: Notice, Text: fmt.Sprintf("context %d%%", int(p*100))})
		}

		calls, stop, err := l.streamTurn(ctx, ms)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				_ = l.emit(Event{Type: Interrupted, Text: "ctrl+c"})
				return nil // an interruption is an outcome, not a failure
			}
			if errors.Is(err, errTurnRateLimited) {
				// The provider reported a 429 this turn. Do NOT reset the
				// consecutive-hits counter — the turn did not succeed. The
				// cooldown at the top of the next loop iteration will wait
				// the appropriate amount, then retry.
				continue
			}
			if errors.Is(err, ErrRateLimitBudget) {
				// CHECK B: mid-stream ceiling hit; emit and surface.
				l.mu.Lock()
				hits2 := l.rateLimitHits
				l.mu.Unlock()
				_ = l.emit(Event{Type: Notice, Text: fmt.Sprintf(
					"rate limit budget exhausted (%d/429s in this run) · wait and retry", hits2)})
				_ = l.emit(Event{Type: RunError, Err: ErrRateLimitBudget.Error()})
				return ErrRateLimitBudget
			}
			// A 413 on Groq is a per-minute TPM violation, not a final
			// failure: waiting a minute resolves it. The human must know,
			// and the requested count N goes into the journal so every
			// future failure feeds the budget equations.
			if notice, ok := tpmLimitNotice(err); ok {
				_ = l.emit(Event{Type: Notice, Text: notice.Text, Limit: notice.Limit, Requested: notice.Requested})
			}
			_ = l.emit(Event{Type: RunError, Err: err.Error()})
			return err
		}

		// A turn that survived provider calls without a 429 is a success: a
		// 429s absorbed earlier in this run are now stale, so the cumulative
		// hit counter is reset. Otherwise a couple of early rate limits
		// followed by healthy turns would let hits drift up to the ceiling
		// and kill a later request the provider was ready to serve — the
		// root cause of the session 62 death (2 initial 429s, then two fine
		// reads, then abort at 3 hits without a third retry).
		l.mu.Lock()
		l.rateLimitHits = 0
		l.rateLimitAttempts = 0
		l.lastRateLimitTime = time.Time{}
		l.mu.Unlock()

		if len(calls) == 0 {
			if stop == "max_tokens" {
				_ = l.emit(Event{Type: Notice, Text: "reached length limit · say \"continue\""})
			}
			return nil
		}

		interrupted, err := l.runCalls(ctx, calls)
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
// It returns errTurnRateLimited (not a fatal error) when the provider
// responded with a 429, so Run() can wait and retry instead of resetting
// the consecutive-429 counter.
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
		MaxTok:   maxOutputTokens(),
	})
	if err != nil {
		return nil, "", err
	}

	var (
		text        string
		calls       []provider.ToolCall
		stop        string
		rateLimited bool // set when the provider emits a 429 this turn
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
			// Record the provider's measured usage and the request parameters
			// for this successful turn. These are the raw inputs needed to
			// derive the charge model: prompt_tokens, completion_tokens,
			// finish_reason, and the max_tokens the client requested.
			_ = l.emit(Event{Type: EventCalib, Calib: &Calibration{
				EncodedBytes: c.EncodedBytes,
				PromptTokens: c.PromptTokens,
				Messages:     len(ms),
			}})
			_ = l.emit(Event{Type: EventProviderUsage, Usage: &ProviderUsage{
				PromptTokens:     c.PromptTokens,
				CompletionTokens: c.CompletionTokens,
				FinishReason:     c.FinishReason,
				RequestMaxTokens: maxOutputTokens(),
				NormalizedStop:   stop,
			}})
			if c.PromptTokens > 0 {
				// Fold the provider's measured input count into the budget
				// ratio — this is the calibration that was dead until now.
				// The adopted ratio is journaled when it moves: the budget
				// is session-varying state, and the log must show which
				// budget the agent worked under.
				if l.Budget.Calibrate(c.PromptTokens, l.Budget.Estimate(ms)) {
					_ = l.emit(Event{Type: Notice, Text: fmt.Sprintf("calibration: token ratio (observed prompt_tokens ÷ heuristic estimate) adopted %.2f · conservative ratchet, rises only (measured prompt_tokens=%d)", l.Budget.Ratio(), c.PromptTokens)})
				}
			}

		case provider.ChunkRateLimit:
			if c.RateLimit != nil {
				l.mu.Lock()
				// Carry the provider's own retry-after through to Run()'s
				// wait, so an explicit "try again in Ns" wins over a
				// generic backoff.
				var retryAfter time.Duration
				if c.RateLimit.WaitSec > 0 {
					retryAfter = time.Duration(c.RateLimit.WaitSec * float64(time.Second))
					l.providerRetryAfter = retryAfter
				} else {
					retryAfter = l.providerRetryAfter
				}
				l.rateLimitHits++
				l.rateLimitAttempts++
				if retryAfter > 0 {
					l.rateLimitTotalWait += retryAfter
				}
				l.lastRateLimitTime = l.clockNow()
				hits := l.rateLimitHits
				totalWait := l.rateLimitTotalWait
				attempts := l.rateLimitAttempts
				l.mu.Unlock()

				_ = l.emit(Event{
					Type:       EventRateLimit,
					Code:       c.RateLimit.Code,
					Limit:      c.RateLimit.Limit,
					Used:       c.RateLimit.Used,
					Requested:  c.RateLimit.Requested,
					WaitSec:    c.RateLimit.WaitSec,
					Attempt:    c.RateLimit.Attempt,
					Err:        c.RateLimit.Err,
					RetryAfter: retryAfter.Seconds(),
					RawMessage: c.RateLimit.RawMessage,
				})

				// Mark this turn as rate-limited so Run() knows not to
				// reset the consecutive-hits counter. The provider no longer
				// sleeps here (it just reports), so the agent owns the wait.
				rateLimited = true

				// A burst of 429s within one turn trips the same absolute
				// guard as the Run() head, so we cannot be stalled here
				// either.
				if totalWait >= 120*time.Second || attempts >= 8 {
					for range ch {
					} // drain so provider goroutine isn't blocked
					return nil, "", ErrRateLimitBudget
				}
				if hits >= l.rateLimitCeiling() {
					for range ch {
					} // drain so provider goroutine isn't blocked
					return nil, "", ErrRateLimitBudget
				}
				// The provider closes the channel after reporting the 429
				// (it does not sleep or retry). The for-loop will exit and
				// we return errTurnRateLimited below.
			}

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
	// A length-cut answer must carry the marker inside the stored text, not
	// only in a Notice: the next turn reads the assistant message and would
	// otherwise build on a truncated answer as if it were complete. The
	// marker sits on its own line, fenced by blank lines, so it never lands
	// inside a code block that the cut may have opened.
	if stop == "max_tokens" && text != "" {
		if err := l.emit(Event{Type: TextDelta, Text: "\n\n[CUT: reached length limit — say \"continue\" to resume]\n\n"}); err != nil {
			return nil, "", err
		}
	}
	if err := l.emit(Event{Type: TurnEnd}); err != nil {
		return nil, "", err
	}
	// If the provider reported a 429 this turn, signal Run() to wait and
	// retry rather than counting this as a success (which would reset the
	// consecutive-429 counter and let hits drift to the ceiling).
	if rateLimited {
		return nil, "", errTurnRateLimited
	}
	return calls, stop, nil
}

// toolNames lists the registered tool names, for error messages that let
// the model correct itself instead of guessing again.
func (l *Loop) toolNames() []string {
	if l.Tools == nil {
		return nil
	}
	var names []string
	for _, s := range l.Tools.Specs() {
		names = append(names, s.Name)
	}
	return names
}

func (l *Loop) knownTool(name string) bool {
	for _, n := range l.toolNames() {
		if n == name {
			return true
		}
	}
	return false
}

// runCalls executes the batch in order. Order matters: the model asked
// for read-then-write for a reason, and parallelism would gain a phone
// nothing but a race.
func (l *Loop) runCalls(ctx context.Context, calls []provider.ToolCall) (bool, error) {
	for _, c := range calls {
		if ctx.Err() != nil {
			return true, nil
		}

		ac := ToolCall{ID: c.ID, Name: c.Name, Args: c.Input}

		if err := l.emit(Event{Type: ToolStart, Call: &ac}); err != nil {
			return false, err
		}

		// An unknown tool is not a permission question: the registry has
		// no such tool, so asking the gate would misfile an existence
		// error under deny and corrupt the evidence. Answer with the
		// tools that DO exist, so the model can correct its next call
		// instead of repeating the same one.
		if !l.knownTool(c.Name) {
			msg := fmt.Sprintf("unknown tool %q · available: %s", c.Name, strings.Join(l.toolNames(), ", "))
			ac.OK, ac.Output = false, msg
			l.emit(Event{Type: ToolEnd, Call: &ac})
			continue
		}

		d, why := l.decide(ctx, ac, l.emit)
		if d == Deny {
			msg := "refused to run " + c.Name
			if why != "" {
				msg += ": " + why
			}
			ac.OK, ac.Output = false, msg
			l.emit(Event{Type: ToolEnd, Call: &ac})
			continue
		}

		start := time.Now()
		out, err := l.exec(ctx, c)
		if err != nil {
			out.Text, out.OK = err.Error(), false
		}
		ms := time.Since(start).Milliseconds()

		if eerr := l.emit(Event{Type: ToolEnd, Call: &ToolCall{
			ID: c.ID, Name: c.Name, Output: out.Text, OK: out.OK,
			Exit: out.Exit, Signal: out.Signal, MS: ms,
		}}); eerr != nil {
			return false, eerr
		}

		// A mutation leaves a persisted fingerprint behind. The loop is the
		// only writer of Seq/Parent, so the EditRecord event is emitted here
		// (not from inside tools, which must not reach into the journal).
		if out.OK && (c.Name == "write_file" || c.Name == "edit_file") {
			if er, ok := l.Tools.(interface{ LastEdit() *EditRecord }); ok {
				if rec := er.LastEdit(); rec != nil {
					if eerr := l.emit(Event{Type: EventEdit, Edit: rec}); eerr != nil {
						return false, eerr
					}
				}
			}
		}

		// A truncated read is a fact the journal must carry: the model saw
		// part of the file, and later replays must know that too.
		if out.OK && c.Name == "read_file" && out.Truncated {
			if eerr := l.emit(Event{Type: EventRead, Read: &ReadRecord{
				Path:       pathOf(c.Input),
				Truncated:  true,
				NextOffset: out.NextOffset,
			}}); eerr != nil {
				return false, eerr
			}
		}
	}
	return false, nil
}

// exec isolates the tool from the loop: a panicking tool must not take
// the conversation down with it.
func (l *Loop) exec(ctx context.Context, c provider.ToolCall) (out Outcome, err error) {
	if l.Tools == nil {
		return Outcome{OK: false}, fmt.Errorf("no tools in this build: %s", c.Name)
	}
	defer func() {
		if r := recover(); r != nil {
			out = Outcome{OK: false}
			err = fmt.Errorf("panic in %s: %v", c.Name, r)
		}
	}()
	if d, ok := l.Tools.(interface {
		RunDetailed(context.Context, string, json.RawMessage) (Outcome, error)
	}); ok {
		return d.RunDetailed(ctx, c.Name, c.Input)
	}
	txt, good, e := l.Tools.Run(ctx, c)
	return Outcome{Text: txt, OK: good}, e
}

type tpmNotice struct {
	Text      string
	Limit     int
	Requested int
}

func tpmLimitNotice(err error) (tpmNotice, bool) {
	var tpm *provider.TPMError
	if !errors.As(err, &tpm) {
		return tpmNotice{}, false
	}
	limit := tpm.Limit
	if limit == 0 {
		limit = 8000
	}
	return tpmNotice{
		Text:      fmt.Sprintf("per-minute limit: %d · requested %d · wait then retry", limit, tpm.Requested),
		Limit:     limit,
		Requested: tpm.Requested,
	}, true
}

// pathOf pulls the path argument out of a tool call's raw JSON, for events
// that want to name the file they touched. Returns "" if unparseable.
func pathOf(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return ""
	}
	return a.Path
}

// emit stamps identity and hands the event on. Seq and Parent are
// assigned here and nowhere else, which is what makes the journal a
// tree rather than a pile. A sink failure is returned to the caller so the
// loop does not continue as if the event was durably recorded.
func (l *Loop) emit(e Event) error {
	return l.emitAt(l.parent, e)
}

// Note lets a human action enter the journal through the same door events
// use, so seq and parent stay the single tree they were designed to be.
// Note failures are logged but do not stop the loop — a notice is not a
// safety-critical event, and crashing the session on a UI blip is worse
// than dropping a status line.
func (l *Loop) Note(text string) {
	_ = l.emit(Event{Type: Notice, Text: text})
}

// End marks the session as finished. It emits exactly one RunEnd event and
// is safe to call multiple times (only the first call emits). Call this
// before closing the journal so the terminal state is durably recorded.
func (l *Loop) End(text string) error {
	l.mu.Lock()
	if l.ended {
		l.mu.Unlock()
		return nil
	}
	l.ended = true
	l.mu.Unlock()
	return l.emit(Event{Type: RunEnd, Text: text})
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
