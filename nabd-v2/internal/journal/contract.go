// Package journal defines the v2 wire contract. Persistence and execution are
// deliberately absent until the phase-0 contract has been reviewed.
package journal

import (
	"encoding/json"
	"time"
)

const SchemaVersion = 1

type Kind string

const (
	SessionStart Kind = "session_start"
	User         Kind = "user"
	Text         Kind = "text"
	ToolCall     Kind = "tool_call"
	ToolResult   Kind = "tool_result"
	PermAsk      Kind = "perm_ask"
	PermReply    Kind = "perm_reply"
	Notice       Kind = "notice"
	Error        Kind = "error"
	Compact      Kind = "compact"
	Rewind       Kind = "rewind"
	FileEdit     Kind = "file_edit"
)

// Seq is strictly increasing in file order; Parent is an earlier event (or
// zero only for session_start). Unknown Kind/Data must survive read/export.
type Event struct {
	Seq    int64           `json:"seq"`
	Parent int64           `json:"parent"`
	Time   time.Time       `json:"time"`
	Kind   Kind            `json:"kind"`
	Data   json.RawMessage `json:"data"`
}

type Class string

const (
	Read    Class = "read"
	Write   Class = "write"
	Execute Class = "execute"
)

type Mode string

const (
	Deny       Mode = "deny"
	AllowReads Mode = "allow-reads"
	Ask        Mode = "ask"
	Plan       Mode = "plan"
)

type Decision string

const (
	Reject       Decision = "deny"
	AllowOnce    Decision = "allow_once"
	AllowSession Decision = "allow_session"
)

type SessionData struct {
	Journal        int    `json:"journal"`
	SessionID      string `json:"session_id"`
	PermissionMode Mode   `json:"permission_mode"`
}

type TextData struct {
	Text   string `json:"text"`
	TurnID string `json:"turn_id,omitempty"`
}

// Args is complete, validated input, never a partial provider-stream delta.
// RequestHash binds consent to canonical name/args + resolved working directory.
// Class recorded in CallData is for audit/replay only. The runtime permission
// gate must derive and enforce tool classification from its own trusted tool
// registry rather than trusting caller-declared Class.
type CallData struct {
	CallID      string          `json:"call_id"`
	TurnID      string          `json:"turn_id"`
	Name        string          `json:"name"`
	Class       Class           `json:"class"`
	Args        json.RawMessage `json:"args"`
	RequestHash string          `json:"request_hash"`
}

type ResultData struct {
	CallID  string `json:"call_id"`
	Output  string `json:"output"`
	IsError bool   `json:"is_error"`
	Code    string `json:"code,omitempty"`
}

type PermissionData struct {
	CallID      string   `json:"call_id"`
	RequestHash string   `json:"request_hash"`
	Decision    Decision `json:"decision,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

// Exists=false represents a missing file, not an empty file.
type FileState struct {
	Exists bool   `json:"exists"`
	SHA256 string `json:"sha256,omitempty"`
}

type Action string

const (
	ActionApply Action = "apply"
	ActionUndo  Action = "undo"
)

func (a Action) IsValid() bool {
	return a == ActionApply || a == ActionUndo
}

// ChangeData is used in the durable intent and in the confirmed file_edit.
// Undo is itself a change; Undoes points to the original apply's event Seq.
type ChangeData struct {
	OperationID string    `json:"operation_id"`
	Path        string    `json:"path"`
	Action      Action    `json:"action"` // apply or undo
	Before      FileState `json:"before"`
	After       FileState `json:"after"`
	Snapshot    string    `json:"snapshot,omitempty"` // hash of Before, if it existed
	Undoes      int64     `json:"undoes,omitempty"`
}

type Status string

const (
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	StatusLimit     Status = "limit"
	StatusUncertain Status = "uncertain"
)

func (s Status) IsValid() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCancelled, StatusLimit, StatusUncertain:
		return true
	default:
		return false
	}
}

// Known codes: turn.end, tool.execution_started, file.change_intent.
// turn.end carries Status=completed|failed|cancelled|limit|uncertain.
// A missing turn.end is interrupted/uncertain, never inferred as success.
type NoticeData struct {
	Code   string      `json:"code"`
	TurnID string      `json:"turn_id,omitempty"`
	Status Status      `json:"status,omitempty"`
	CallID string      `json:"call_id,omitempty"`
	Change *ChangeData `json:"change,omitempty"`
}

// ValidateStatus ensures Status is valid for turn.end notices, and empty for all other notice codes.
func (n NoticeData) ValidateStatus() bool {
	if n.Code == "turn.end" {
		return n.Status.IsValid()
	}
	return n.Status == ""
}

type ErrorData struct {
	Code   string `json:"code"`
	Hint   string `json:"hint"`
	TurnID string `json:"turn_id,omitempty"`
}

type CompactData struct {
	Summary string `json:"summary"`
}

// Parent is the retained head; PreviousHead identifies the abandoned branch.
// No event or file is deleted. Rewinding is not undoing.
type RewindData struct {
	PreviousHead int64 `json:"previous_head"`
}
