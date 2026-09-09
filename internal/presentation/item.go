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

// Fingerprint returns a deterministic hash of all fields that affect rendering.
// It is pure (no UI state) and uses length-prefixed encoding to avoid ambiguity
// (e.g., ["ab","c"] ≠ ["a","bc"]). Fields that don't affect display (like Seq)
// are excluded by design — changing them must not change the fingerprint.
func (it FeedItem) Fingerprint() uint64 {
	var h uint64 = 1469598103934665603 // FNV-1a offset basis

	// Type determines how the item is rendered.
	hashString(&h, string(it.Type))

	// Text is rendered for user/assistant/notice/error/run_boundary items.
	hashString(&h, it.Text)

	// RunBoundary marks session start/end with a visible separator.
	hashString(&h, it.RunBoundary)

	// Tool card: all fields are visible in the rendered output.
	if it.Tool != nil {
		hashString(&h, it.Tool.Name)
		hashString(&h, it.Tool.Args)
		hashString(&h, string(it.Tool.Status))
		hashString(&h, it.Tool.Output)
		hashInt64(&h, it.Tool.Duration)
		hashInt(&h, it.Tool.ExitCode)
		hashString(&h, it.Tool.Signal)
		hashString(&h, it.Tool.Err)
		hashBool(&h, it.Tool.Truncated)
	}

	// Perm card: all fields affect the rendered permission display.
	if it.Perm != nil {
		hashString(&h, it.Perm.Name)
		hashString(&h, it.Perm.Args)
		hashString(&h, string(it.Perm.Status))
		hashString(&h, string(it.Perm.Decision))
		hashString(&h, string(it.Perm.Effective))
	}

	return h
}

// hashString incorporates a length-prefixed string into the hash.
// Length prefixing prevents ["ab","c"] from colliding with ["a","bc"].
func hashString(h *uint64, s string) {
	*h ^= uint64(len(s))
	*h *= 1099511628211 // FNV-1a prime
	for _, b := range []byte(s) {
		*h ^= uint64(b)
		*h *= 1099511628211
	}
}

// hashInt64 incorporates an int64 into the hash.
func hashInt64(h *uint64, v int64) {
	*h ^= uint64(v)
	*h *= 1099511628211
}

// hashInt incorporates an int into the hash.
func hashInt(h *uint64, v int) {
	*h ^= uint64(v)
	*h *= 1099511628211
}

// hashBool incorporates a bool into the hash.
func hashBool(h *uint64, v bool) {
	if v {
		*h ^= 1
	} else {
		*h ^= 0
	}
	*h *= 1099511628211
}

// sortBySeq orders items using their Seq field, preserving the original
// event order. Items without Seq (0) or with identical Seq keep their relative positions.
func sortBySeq(items []FeedItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Seq != 0 && items[j].Seq != 0 && items[i].Seq != items[j].Seq {
			return items[i].Seq < items[j].Seq
		}
		return false
	})
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
