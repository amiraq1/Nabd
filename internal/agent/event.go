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
const MaxPersistedOutput = 16384

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
	Compact     EventType = "compact"
	Rewind      EventType = "rewind"
	EventEdit   EventType = "edit_record"
	EventRead   EventType = "read_record"
	EventCalib     EventType = "calibration"
	EventRateLimit EventType = "rate_limit"
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

	Text      string       `json:"text,omitempty"`
	Call      *ToolCall    `json:"call,omitempty"`
	Decision  Decision     `json:"decision,omitempty"`
	Err       string       `json:"err,omitempty"`
	Code      int          `json:"code,omitempty"`
	Limit     int          `json:"limit,omitempty"`
	Used      int          `json:"used,omitempty"`
	Requested int          `json:"requested,omitempty"`
	WaitSec   float64      `json:"wait_s,omitempty"`
	Attempt   int          `json:"attempt,omitempty"`
	Edit      *EditRecord  `json:"edit,omitempty"`
	Read      *ReadRecord  `json:"read,omitempty"`
	Calib     *Calibration `json:"calib,omitempty"`

	FirstKept int              `json:"first_kept,omitempty"`
	Compact   *CompactionStats `json:"compact,omitempty"`
}

// CompactionStats records the context measurements at each actual compaction.
type CompactionStats struct {
	MessagesBefore int `json:"messages_before"`
	MessagesAfter  int `json:"messages_after"`
	TokensBefore   int `json:"tokens_before"`
	TokensAfter    int `json:"tokens_after"`
	BoundaryIndex  int `json:"boundary_index"`
	Stubs          int `json:"stubs"`
}

// Calibration is one measurement point for the budget regression: the
// encoded request bytes, the provider's measured prompt tokens, and the
// message count sent. Two such points solve ratio and overhead by
// regression. It is journal-only — Messages() ignores it, so it never
// reaches the model or the human screen.
type Calibration struct {
	EncodedBytes int `json:"encoded_bytes"`
	PromptTokens int `json:"prompt_tokens"`
	Messages     int `json:"messages"`
}

// ReadRecord describes one read_file call. Truncated is true only when the
// byte cap cut the file short — the model must be able to tell "full read"
// from "partial read" from the event itself. NextOffset is the exact line
// to continue from, in the same unit read_file's offset param accepts.
type ReadRecord struct {
	Path       string `json:"path"`
	Truncated  bool   `json:"truncated"`
	NextOffset int    `json:"next_offset,omitempty"`
}

// EditRecord is the persisted fingerprint of one file mutation. It is the
// journal side of /undo: hashes let a later process verify the file is
// untouched before restoring, and the patch is the human-readable proof.
// Full file copies are never stored here — the shadow holds content.
type EditRecord struct {
	Path       string `json:"path"`
	HashBefore string `json:"hash_before"`
	HashAfter  string `json:"hash_after"`
	Patch      string `json:"patch"`
	ReadLines  int    `json:"read_lines"`
	// BlobBefore/BlobAfter are the shadow's content keys (git oid or
	// s256:…). They are what a restarted /undo needs to pull content back;
	// the SHA-256 hashes alone cannot address git's object store.
	BlobBefore string `json:"blob_before,omitempty"`
	BlobAfter  string `json:"blob_after,omitempty"`
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

// Outcome is what a tool run produced. Text-only tools ignore the extra
// fields; a process cannot, because "failed" and "killed at 120s" are
// different facts and the model must be able to tell them apart.
type Outcome struct {
	Text      string
	OK        bool
	Exit      int
	Signal    string
	Truncated bool // read_file set this: the byte cap cut the file short
	// NextOffset is the exact line to continue from (read_file truncation).
	NextOffset int
}
