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

type ToolStatus string

const (
	ToolPending   ToolStatus = "pending"
	ToolRunning   ToolStatus = "running"
	ToolDone      ToolStatus = "done"
	ToolFailed    ToolStatus = "failed"
	ToolDenied    ToolStatus = "denied"
	ToolCancelled ToolStatus = "cancelled"
)

// OutputState says which part of a tool result is available for expansion.
// Execution truncation is represented separately by ToolCard.Truncated because
// a read can be complete in the journal yet still represent only part of its
// source file. OutputTruncated means the journal itself retained only a prefix.
type OutputState string

const (
	OutputNone        OutputState = "none"
	OutputSaved       OutputState = "saved"
	OutputTruncated   OutputState = "truncated"
	OutputUnavailable OutputState = "unavailable"
)

type PermStatus string

const (
	PermAsked PermStatus = "asked"
	PermAllow PermStatus = "allowed"
	PermDeny  PermStatus = "denied"
)

type ToolCard struct {
	CallID      string
	Name        string
	Args        string
	Status      ToolStatus
	Output      string
	OutputState OutputState
	Duration    int64
	ExitCode    int
	Signal      string
	Err         string
	// Truncated is execution-level truncation. OutputTruncated is persistence-level.
	Truncated  bool
	NextOffset *int
}

type PermCard struct {
	Name      string
	Args      string
	Status    PermStatus
	Decision  agent.Decision
	Effective agent.Decision
}

type FeedItem struct {
	Type        ItemType   `json:"type"`
	ID          string     `json:"id"`
	Seq         int        `json:"seq"`
	Text        string     `json:"text"`
	Tool        *ToolCard  `json:"tool,omitempty"`
	Perm        *PermCard  `json:"permission,omitempty"`
	Error       *ErrorCard `json:"error,omitempty"`
	RunBoundary string     `json:"run_boundary,omitempty"`
}

func (it FeedItem) key() string { return fmt.Sprintf("%s:%s", it.Type, it.ID) }

func (it FeedItem) Fingerprint() uint64 {
	var h uint64 = 1469598103934665603
	hashString(&h, string(it.Type))
	hashString(&h, it.Text)
	hashString(&h, it.RunBoundary)
	if it.Tool != nil {
		hashString(&h, it.Tool.CallID)
		hashString(&h, it.Tool.Name)
		hashString(&h, it.Tool.Args)
		hashString(&h, string(it.Tool.Status))
		hashString(&h, it.Tool.Output)
		hashString(&h, string(it.Tool.OutputState))
		hashInt64(&h, it.Tool.Duration)
		hashInt(&h, it.Tool.ExitCode)
		hashString(&h, it.Tool.Signal)
		hashString(&h, it.Tool.Err)
		hashBool(&h, it.Tool.Truncated)
		if it.Tool.NextOffset != nil {
			hashBool(&h, true)
			hashInt(&h, *it.Tool.NextOffset)
		} else {
			hashBool(&h, false)
		}
	}
	if it.Error != nil {
		hashString(&h, string(it.Error.Code))
		hashString(&h, it.Error.Title)
		hashString(&h, it.Error.Message)
		hashString(&h, it.Error.ActionText)
		hashBool(&h, it.Error.Retryable)
		hashString(&h, string(it.Error.RetryScope))
		hashString(&h, it.Error.JournalPath)
		hashBool(&h, it.Error.ToolExecuted)
	}
	if it.Perm != nil {
		hashString(&h, it.Perm.Name)
		hashString(&h, it.Perm.Args)
		hashString(&h, string(it.Perm.Status))
		hashString(&h, string(it.Perm.Decision))
		hashString(&h, string(it.Perm.Effective))
	}
	return h
}

func hashString(h *uint64, s string) {
	*h ^= uint64(len(s))
	*h *= 1099511628211
	for _, b := range []byte(s) {
		*h ^= uint64(b)
		*h *= 1099511628211
	}
}
func hashInt64(h *uint64, v int64) { *h ^= uint64(v); *h *= 1099511628211 }
func hashInt(h *uint64, v int)     { *h ^= uint64(v); *h *= 1099511628211 }
func hashBool(h *uint64, v bool) {
	if v {
		*h ^= 1
	}
	*h *= 1099511628211
}

func sortBySeq(items []FeedItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Seq != 0 && items[j].Seq != 0 && items[i].Seq != items[j].Seq {
			return items[i].Seq < items[j].Seq
		}
		return false
	})
}

func callArgs(c *agent.ToolCall) string {
	if c == nil {
		return ""
	}
	m := rawToMap(c.Args)
	if len(m) == 0 {
		return ""
	}
	for _, k := range []string{"cmd", "path", "pattern", "query"} {
		if v, ok := m[k]; ok {
			return fmt.Sprint(v)
		}
	}
	for _, v := range m {
		return fmt.Sprint(v)
	}
	return ""
}

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
