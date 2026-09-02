// Package agent: compact.go writes one entry and changes what the session
// means. It never rewrites the file: first_kept moves the boundary, Live()
// obeys it, and every dropped event stays on disk for the replay.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"nabd/internal/provider"
)

const summaryPrompt = `You are summarising a coding session so it can continue after context compaction.
Write a brief summary mentioning: what the user asked, what was actually done, files read or modified by name, decisions taken and why, and what remains pending.
Do not apologise, do not greet, do not invent what did not happen. Leave out points you do not know.`

// Compact cuts history at the newest user turn whose tail fits in target,
// summarises everything before it, and appends one Compact entry.
func (l *Loop) Compact(ctx context.Context, target int) error {
	l.mu.Lock()
	live := Live(l.hist)
	l.mu.Unlock()

	firstKept, dropped, ok := chooseBoundary(live, target)
	if !ok {
		return fmt.Errorf("no valid boundary (%d live events)", len(live))
	}
	sum := l.summarise(ctx, dropped)
	l.emit(Event{Type: Compact, FirstKept: firstKept, Text: sum})
	return nil
}

// chooseBoundary walks user turns from newest to oldest and takes the oldest
// tail that still fits. The boundary is always a user message: cutting inside
// a round would leave a tool_result whose tool_use no longer exists, and the
// next request would be rejected outright.
func chooseBoundary(live []Event, target int) (int, []Event, bool) {
	var idx []int
	for i, e := range live {
		if e.Type == UserMsg {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		return 0, nil, false
	}
	best := -1
	for k := len(idx) - 1; k >= 0; k-- {
		i := idx[k]
		if EstimateMessages(Messages(live[i:])) > target && best >= 0 {
			break
		}
		best = i
	}
	if best <= 0 { // nothing older than the newest turn: not worth an entry
		return 0, nil, false
	}
	return live[best].Seq, live[:best], true
}

func (l *Loop) summarise(ctx context.Context, dropped []Event) string {
	mech := mechanicalSummary(dropped)
	if l.Provider == nil {
		return mech
	}
	ms := append(Messages(dropped), provider.Message{
		Role: provider.User,
		Text: "Summarise what came before per the instructions.",
	})
	ch, err := l.Provider.Stream(ctx, provider.Request{
		Messages: ms,
		System:   summaryPrompt,
	})
	if err != nil {
		return mech
	}
	var b strings.Builder
	for c := range ch {
		if c.Text != "" {
			b.WriteString(c.Text)
		}
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		return mech // a failed summariser must never cost the thread
	}
	return s + "\n\n" + mech
}

// argSummaryPath extracts the 'path' key from raw JSON args.
func argSummaryPath(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(args, &m) != nil {
		return ""
	}
	if v, ok := m["path"]; ok {
		return fmt.Sprint(v)
	}
	return ""
}

// mechanicalSummary is the floor: no network, no model, always available.
// The verbatim user turns matter most — intent is the one thing a paraphrase
// is most likely to bend.
func mechanicalSummary(evs []Event) string {
	var asks []string
	files := map[string]bool{}
	errs := 0
	for _, e := range evs {
		switch e.Type {
		case UserMsg:
			t := e.Text
			if len(t) > 160 {
				t = t[:160] + "…"
			}
			asks = append(asks, "- "+t)
		case ToolEnd:
			if e.Call == nil {
				continue
			}
			if p := argSummaryPath(e.Call.Args); p != "" {
				files[p] = true
			}
			if !e.Call.OK {
				errs++
			}
		}
	}
	var b strings.Builder
	b.WriteString("«brief log»\nUser requests:\n")
	b.WriteString(strings.Join(asks, "\n"))
	if len(files) > 0 {
		var fs []string
		for f := range files {
			fs = append(fs, f)
		}
		fmt.Fprintf(&b, "\nFiles touched by tools: %s", strings.Join(fs, ", "))
	}
	if errs > 0 {
		fmt.Fprintf(&b, "\nTool errors: %d", errs)
	}
	return b.String()
}
