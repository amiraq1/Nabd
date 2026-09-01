// Package agent defines the event contract. It is the only thing every
// other package agrees on: if it is not an Event, it did not happen.
package agent

import (
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"
)

// MaxPersistedOutput caps tool output on disk, not on screen.
const MaxPersistedOutput = 4096

type EventType string

const (
	RunStart    EventType = "run_start"
	UserMsg     EventType = "user_msg"
	TurnStart   EventType = "turn_start"
	TextDelta   EventType = "text_delta"
	ToolStart   EventType = "tool_start"
	PermAsk     EventType = "perm_ask"
	PermReply   EventType = "perm_reply"
	ToolEnd     EventType = "tool_end"
	Notice      EventType = "notice"
	RunError    EventType = "run_error"
	Interrupted EventType = "interrupted"
	TurnEnd     EventType = "turn_end"
	RunEnd      EventType = "run_end"
	Compact     EventType = "compact"
)

// Event is one line in the journal. Append-only, never rewritten.
//
// Seq is the identity: monotonic within a file, and what Parent points at.
// Parent is the previous event on the same branch; 0 means root. A rewind
// or a fork writes a new event whose Parent is an older Seq, which makes
// the file a tree without ever touching a byte already written.
type Event struct {
	Seq    int       `json:"seq"`
	Parent int       `json:"parent,omitempty"`
	Time   time.Time `json:"t"`
	Type   EventType `json:"type"`

	Text     string    `json:"text,omitempty"`
	Call     *ToolCall `json:"call,omitempty"`
	Decision Decision  `json:"decision,omitempty"`
	Err      string    `json:"err,omitempty"`

	// FirstKept is set on Compact only: the oldest Seq that survives.
	FirstKept int `json:"first_kept,omitempty"`
}

// ToolCall carries both the request and its outcome.
type ToolCall struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args,omitempty"`
	Output string          `json:"out,omitempty"`
	OK     bool            `json:"ok,omitempty"`
	Exit   int             `json:"exit,omitempty"`
	Signal string          `json:"signal,omitempty"`
	MS     int64           `json:"ms,omitempty"`
}

// Decision is fail-closed by construction: the zero value refuses.
type Decision uint8

const (
	Deny Decision = iota
	AllowOnce
	AllowSession
)

func (d Decision) String() string {
	switch d {
	case AllowOnce:
		return "once"
	case AllowSession:
		return "session"
	default:
		return "deny"
	}
}

func (d Decision) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func (d *Decision) UnmarshalText(b []byte) error {
	switch string(b) {
	case "once":
		*d = AllowOnce
	case "session":
		*d = AllowSession
	default:
		*d = Deny
	}
	return nil
}

// ForStore returns the event as written to disk: identical, except that
// oversized tool output is cut on a rune boundary and marked. Text deltas
// are never touched, or replay stops being replay.
func (e Event) ForStore() Event {
	if e.Call == nil || len(e.Call.Output) <= MaxPersistedOutput {
		return e
	}
	c := *e.Call
	n := MaxPersistedOutput
	for n > 0 && !utf8.RuneStart(c.Output[n]) {
		n--
	}
	cut := len(c.Output) - n
	c.Output = c.Output[:n] + fmt.Sprintf("\n...[truncated %d bytes]", cut)
	e.Call = &c
	return e
}

// Live returns the events that replay and the context window should see:
// the branch ending at the last written event, starting at the newest
// compaction boundary, with the summary at the head.
func Live(events []Event) []Event {
	if len(events) == 0 {
		return nil
	}
	by := make(map[int]Event, len(events))
	for _, e := range events {
		by[e.Seq] = e
	}

	var branch []Event
	cur := events[len(events)-1]
	for {
		branch = append(branch, cur)
		p, ok := by[cur.Parent]
		if cur.Parent == 0 || !ok || p.Seq >= cur.Seq {
			break
		}
		cur = p
	}
	for i, j := 0, len(branch)-1; i < j; i, j = i+1, j-1 {
		branch[i], branch[j] = branch[j], branch[i]
	}

	ci := -1
	for i := len(branch) - 1; i >= 0; i-- {
		if branch[i].Type == Compact {
			ci = i
			break
		}
	}
	if ci < 0 {
		return branch
	}
	live := make([]Event, 0, len(branch)-ci)
	live = append(live, branch[ci])
	for _, e := range branch {
		if e.Type != Compact && e.Seq >= branch[ci].FirstKept {
			live = append(live, e)
		}
	}
	return live
}
