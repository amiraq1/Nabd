// Package presentation turns the agent's event journal into a stable,
// UI-independent feed. It knows nothing about Bubble Tea or lipgloss: give it
// events, get items. The UI layer later decides how to paint those items.
//
// Two entry points are provided:
//
//	Build(events)  -> []FeedItem   // whole journal at once
//	Apply(event)   -> error        // incremental; read result via Items()
//
// Both produce identical output for the same event stream.
package presentation

import (
	"encoding/json"
	"fmt"
	"sort"

	"nabd/internal/agent"
)

// ItemType is one kind of feed element the projector can emit.
type ItemType string

const (
	ItemUserMsg     ItemType = "user_msg"
	ItemAssistant   ItemType = "assistant"
	ItemTool        ItemType = "tool"
	ItemPermission  ItemType = "permission"
	ItemNotice      ItemType = "notice"
	ItemError       ItemType = "error"
	ItemRunBoundary ItemType = "run_boundary"
)

// ToolStatus tracks where a tool call is in its lifecycle.
type ToolStatus string

const (
	ToolPending   ToolStatus = "pending"
	ToolRunning   ToolStatus = "running"
	ToolDone      ToolStatus = "done"
	ToolFailed    ToolStatus = "failed"
	ToolDenied    ToolStatus = "denied"
	ToolCancelled ToolStatus = "cancelled"
)

// PermStatus tracks a permission request.
type PermStatus string

const (
	PermAsked PermStatus = "asked"
	PermAllow PermStatus = "allowed"
	PermDeny  PermStatus = "denied"
)

// ToolCard holds everything the UI needs to render one tool call.
type ToolCard struct {
	Name      string
	Args      string // human-readable arg summary, not raw JSON
	Status    ToolStatus
	Output    string
	Duration  int64 // milliseconds
	ExitCode  int
	Signal    string
	Err       string
	Truncated bool
}

// PermCard holds one permission request/answer pair.
type PermCard struct {
	Name      string
	Args      string
	Status    PermStatus
	Decision  agent.Decision
	Effective agent.Decision
}

// FeedItem is one element in the rendered feed. Exactly one of the pointer
// fields is non-nil; the ItemType says which.
type FeedItem struct {
	Type ItemType `json:"type"`
	ID   string   `json:"id"`  // stable identity for diffing/keying
	Seq  int      `json:"seq"` // source event Seq, for ordering/debugging

	Text string `json:"text"` // for user/assistant/notice/error/run_boundary

	Tool *ToolCard `json:"tool,omitempty"`
	Perm *PermCard `json:"permission,omitempty"`

	// RunBoundary marks the start/end of a session run.
	RunBoundary string `json:"run_boundary,omitempty"` // "start" | "end"
}

// key returns a stable identity for a feed item so the UI can diff.
func (it FeedItem) key() string {
	return fmt.Sprintf("%s:%s", it.Type, it.ID)
}

// sortableItems is a private type used only for deterministic ordering.
type sortableItems struct {
	items []FeedItem
	seqs  map[string]int
}

func (s sortableItems) Len() int      { return len(s.items) }
func (s sortableItems) Swap(i, j int) { s.items[i], s.items[j] = s.items[j], s.items[i] }
func (s sortableItems) Less(i, j int) bool {
	// Preserve source event order when we have seq info.
	si, sok := s.seqs[s.items[i].key()]
	sj, sokj := s.seqs[s.items[j].key()]
	if sok && sokj && si != sj {
		return si < sj
	}
	return s.items[i].ID < s.items[j].ID
}

// sortBySeq orders items using their Seq field, preserving the original
// event order. Items without Seq (0) keep their relative positions.
func sortBySeq(items []FeedItem) {
	seqs := map[string]int{}
	for _, it := range items {
		if it.Seq != 0 {
			seqs[it.key()] = it.Seq
		}
	}
	sort.Sort(sortableItems{items: items, seqs: seqs})
}

// argSummary produces a one-line human-readable summary of a tool call's
// arguments, without dumping raw JSON to the screen.
func callArgs(c *agent.ToolCall) string {
	if c == nil {
		return ""
	}
	m := rawToMap(c.Args)
	if len(m) == 0 {
		return ""
	}
	// Show the one argument a human actually wants to see, in priority order.
	for _, k := range []string{"cmd", "path", "pattern", "query"} {
		if v, ok := m[k]; ok {
			return fmt.Sprint(v)
		}
	}
	// Fallback: first value.
	for _, v := range m {
		return fmt.Sprint(v)
	}
	return ""
}

// rawToMap unmarshals raw JSON args into a flat map for display.
func rawToMap(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}
