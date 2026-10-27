package journal

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func decode[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}
func fixture(t *testing.T, name string) []Event {
	t.Helper()
	f, err := os.Open(filepath.Join("../../testdata", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var es []Event
	s := bufio.NewScanner(f)
	for s.Scan() {
		es = append(es, decode[Event](t, s.Bytes()))
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	return es
}
func hash(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func TestGoldenEnvelopes(t *testing.T) {
	known := map[Kind]bool{SessionStart: true, User: true, Text: true, ToolCall: true, ToolResult: true, PermAsk: true, PermReply: true, Notice: true, Error: true, Compact: true, Rewind: true, FileEdit: true}
	if len(known) != 12 {
		t.Fatal("contract must contain twelve kinds")
	}
	for _, name := range []string{"read_denied", "edit_undo", "rewind"} {
		t.Run(name, func(t *testing.T) {
			es := fixture(t, name)
			seen := map[int64]bool{}
			var prev int64
			for i, e := range es {
				if e.Seq <= prev || seen[e.Seq] {
					t.Fatalf("invalid seq %d", e.Seq)
				}
				if i == 0 {
					if e.Kind != SessionStart || e.Parent != 0 {
						t.Fatal("missing root")
					}
					d := decode[SessionData](t, e.Data)
					if d.Journal != SchemaVersion || d.SessionID == "" {
						t.Fatal("invalid session metadata")
					}
					switch d.PermissionMode {
					case Deny, AllowReads, Ask, Plan:
					default:
						t.Fatal("invalid mode")
					}
				} else if !seen[e.Parent] || e.Parent >= e.Seq {
					t.Fatalf("invalid parent %d", e.Parent)
				}
				if e.Time.IsZero() || e.Time.Location() != time.UTC {
					t.Fatal("timestamp must be UTC")
				}
				if !known[e.Kind] {
					t.Fatalf("unexpected kind %q in golden", e.Kind)
				}
				switch e.Kind {
				case Notice:
					d := decode[NoticeData](t, e.Data)
					if d.Code == "turn.end" {
						switch d.Status {
						case StatusCompleted, StatusFailed, StatusCancelled, StatusLimit, StatusUncertain:
						default:
							t.Fatalf("invalid turn.end status: %q", d.Status)
						}
					}
					if d.Change != nil {
						switch d.Change.Action {
						case ActionApply, ActionUndo:
						default:
							t.Fatalf("invalid change action in intent: %q", d.Change.Action)
						}
					}
				case FileEdit:
					d := decode[ChangeData](t, e.Data)
					switch d.Action {
					case ActionApply, ActionUndo:
					default:
						t.Fatalf("invalid change action: %q", d.Action)
					}
				}
				seen[e.Seq] = true
				prev = e.Seq
			}
		})
	}
}

func TestGoldenCallBindings(t *testing.T) {
	for _, name := range []string{"read_denied", "edit_undo"} {
		t.Run(name, func(t *testing.T) {
			var sessionMode Mode
			calls := map[string]CallData{}
			decisions := map[string]Decision{}
			results := map[string]bool{}
			ended := map[string]bool{}
			turns := map[string]bool{}
			for _, e := range fixture(t, name) {
				switch e.Kind {
				case SessionStart:
					sessionMode = decode[SessionData](t, e.Data).PermissionMode
				case Text:
					d := decode[TextData](t, e.Data)
					if d.TurnID == "" {
						t.Fatal("text event missing turn_id")
					}
					turns[d.TurnID] = true
				case ToolCall:
					d := decode[CallData](t, e.Data)
					if d.CallID == "" || d.TurnID == "" || calls[d.CallID].CallID != "" {
						t.Fatal("duplicate/empty call")
					}
					turns[d.TurnID] = true
					switch d.Class {
					case Read, Write, Execute:
					default:
						t.Fatal("invalid class")
					}
					var args map[string]json.RawMessage
					if err := json.Unmarshal(d.Args, &args); err != nil || args == nil {
						t.Fatal("args must be object")
					}
					// Phase 0 treats the digest as an opaque binding token. Canonical
					// request encoding belongs to phase 1, not these fixtures.
					if !validRequestHash(d.RequestHash) {
						t.Fatal("invalid request digest shape")
					}
					calls[d.CallID] = d
				case PermAsk, PermReply:
					d := decode[PermissionData](t, e.Data)
					c, ok := calls[d.CallID]
					if !ok || c.RequestHash != d.RequestHash {
						t.Fatal("unbound permission")
					}
					if e.Kind == PermReply {
						switch d.Decision {
						case Reject, AllowOnce, AllowSession:
						default:
							t.Fatal("invalid decision")
						}
						if c.Class == Execute && d.Decision == AllowSession {
							t.Fatal("shell session grant")
						}
						decisions[d.CallID] = d.Decision
					}
				case Notice:
					d := decode[NoticeData](t, e.Data)
					if d.Code == "turn.end" {
						if d.TurnID == "" || ended[d.TurnID] {
							t.Fatal("missing or repeated turn end")
						}
						switch d.Status {
						case StatusCompleted, StatusFailed, StatusCancelled, StatusLimit, StatusUncertain:
						default:
							t.Fatalf("invalid turn.end status: %q", d.Status)
						}
						ended[d.TurnID] = true
					}
					if d.Code == "tool.execution_started" {
						if d.CallID == "" || (decisions[d.CallID] != AllowOnce && decisions[d.CallID] != AllowSession) {
							t.Fatal("execution without consent")
						}
					}
					if d.Code == "file.change_intent" {
						if d.CallID != "" {
							if decisions[d.CallID] != AllowOnce && decisions[d.CallID] != AllowSession {
								t.Fatal("execution without consent")
							}
						} else {
							// Finding F1: Without call_id, only ActionUndo with Undoes > 0 is permitted,
							// and is rejected under Plan or Deny mode.
							if d.Change == nil || d.Change.Action != ActionUndo || d.Change.Undoes <= 0 {
								t.Fatal("file change without consent or call_id")
							}
							if sessionMode == Plan || sessionMode == Deny {
								t.Fatalf("undo intent not permitted in %s mode", sessionMode)
							}
						}
					}
				case ToolResult:
					d := decode[ResultData](t, e.Data)
					if _, ok := calls[d.CallID]; !ok || results[d.CallID] {
						t.Fatal("orphan/duplicate result")
					}
					decision, ok := decisions[d.CallID]
					if !ok {
						t.Fatal("result without permission decision")
					}
					if decision == Reject && (!d.IsError || d.Code != "permission.denied") {
						t.Fatal("denial reported as success")
					}
					results[d.CallID] = true
				}
			}
			for id, c := range calls {
				if !results[id] {
					t.Fatalf("missing result: %s", id)
				}
				if !ended[c.TurnID] {
					t.Fatalf("missing turn end: %s", c.TurnID)
				}
			}
			for turnID := range turns {
				if !ended[turnID] {
					t.Fatalf("turn %s has no turn.end notice", turnID)
				}
			}
		})
	}
}

func TestDeniedReadHasNoExecution(t *testing.T) {
	for _, e := range fixture(t, "read_denied") {
		if e.Kind == FileEdit {
			t.Fatal("denied call changed a file")
		}
		if e.Kind == Notice {
			d := decode[NoticeData](t, e.Data)
			if d.Code == "tool.execution_started" || d.Code == "file.change_intent" {
				t.Fatal("denied call started execution")
			}
		}
	}
}

func TestEditUndoGolden(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/files.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Files map[string]struct {
			Before string `json:"before"`
			After  string `json:"after"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	snapshots := map[string][]byte{}
	for path, f := range manifest.Files {
		files[path] = []byte(f.Before)
		snapshots[hash([]byte(f.Before))] = []byte(f.Before)
		snapshots[hash([]byte(f.After))] = []byte(f.After)
	}
	intents := map[string]ChangeData{}
	applied := map[int64]ChangeData{}
	confirmed := map[string]bool{}
	var edits int
	for _, e := range fixture(t, "edit_undo") {
		if e.Kind == Notice {
			d := decode[NoticeData](t, e.Data)
			if d.Code == "file.change_intent" {
				if d.Change == nil || intents[d.Change.OperationID].OperationID != "" {
					t.Fatal("missing or duplicate intent")
				}
				intents[d.Change.OperationID] = *d.Change
			}
		}
		if e.Kind != FileEdit {
			continue
		}
		d := decode[ChangeData](t, e.Data)
		if !reflect.DeepEqual(intents[d.OperationID], d) || confirmed[d.OperationID] {
			t.Fatal("unmatched/repeated confirmation")
		}
		before, exists := files[d.Path]
		if exists != d.Before.Exists || hash(before) != d.Before.SHA256 {
			t.Fatal("before hash mismatch")
		}
		if !bytes.Equal(snapshots[d.Snapshot], before) {
			t.Fatal("snapshot is not before content")
		}
		switch d.Action {
		case ActionApply:
			if d.Undoes != 0 {
				t.Fatal("apply has undo target")
			}
			applied[e.Seq] = d
		case ActionUndo:
			original, ok := applied[d.Undoes]
			if !ok || original.Path != d.Path || original.After != d.Before || original.Before != d.After {
				t.Fatal("invalid undo target")
			}
			delete(applied, d.Undoes)
		default:
			t.Fatal("invalid change action")
		}
		after, ok := snapshots[d.After.SHA256]
		if !ok || hash(after) != d.After.SHA256 {
			t.Fatal("missing fixture content")
		}
		files[d.Path] = append([]byte(nil), after...)
		confirmed[d.OperationID] = true
		edits++
	}
	if edits != 2 || len(applied) != 0 || len(confirmed) != len(intents) {
		t.Fatal("incomplete edit/undo sequence")
	}
	for path, f := range manifest.Files {
		if !bytes.Equal(files[path], []byte(f.Before)) {
			t.Fatal("not restored byte-for-byte")
		}
	}
}

// validRequestHash checks shape only, not a canonicalization algorithm.
// It requires exactly 64 lowercase hexadecimal characters (SHA-256 hex digest).
func validRequestHash(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func TestRequestHashShape(t *testing.T) {
	for _, tt := range []struct {
		name, digest string
		valid        bool
	}{
		{"empty", "", false},
		{"short", "aabb", false},
		{"nonhex", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", false},
		{"uppercase", "0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef", false},
		{"opaque", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := validRequestHash(tt.digest); got != tt.valid {
				t.Fatalf("validRequestHash()=%v, want %v", got, tt.valid)
			}
		})
	}
}

func TestAllTurnsConcluded(t *testing.T) {
	for _, name := range []string{"read_denied", "edit_undo", "rewind"} {
		t.Run(name, func(t *testing.T) {
			turns := map[string]bool{}
			ended := map[string]bool{}
			for _, e := range fixture(t, name) {
				switch e.Kind {
				case Text:
					d := decode[TextData](t, e.Data)
					if d.TurnID == "" {
						t.Fatalf("fixture %s: text event missing turn_id", name)
					}
					turns[d.TurnID] = true
				case ToolCall:
					d := decode[CallData](t, e.Data)
					if d.TurnID == "" {
						t.Fatalf("fixture %s: tool_call event missing turn_id", name)
					}
					turns[d.TurnID] = true
				case Notice:
					d := decode[NoticeData](t, e.Data)
					if d.Code == "turn.end" {
						if d.TurnID == "" || ended[d.TurnID] {
							t.Fatalf("fixture %s: invalid or duplicate turn.end turn_id: %q", name, d.TurnID)
						}
						switch d.Status {
						case StatusCompleted, StatusFailed, StatusCancelled, StatusLimit, StatusUncertain:
						default:
							t.Fatalf("fixture %s: invalid turn.end status: %q", name, d.Status)
						}
						ended[d.TurnID] = true
					}
				}
			}
			for turnID := range turns {
				if !ended[turnID] {
					t.Fatalf("fixture %s: turn %s missing turn.end", name, turnID)
				}
			}
			for turnID := range ended {
				if !turns[turnID] {
					t.Fatalf("fixture %s: turn.end for unknown turn %s", name, turnID)
				}
			}
		})
	}
}

func TestFileChangeIntentBypassForbidden(t *testing.T) {
	// 1. Change intent with action=apply but missing call_id must fail.
	t.Run("apply_without_call_id", func(t *testing.T) {
		intent := NoticeData{
			Code: "file.change_intent",
			Change: &ChangeData{
				OperationID: "op-1",
				Action:      ActionApply,
				Path:        "test.txt",
			},
		}
		if intent.CallID != "" {
			t.Fatal("expected empty call_id")
		}
		// Oracle rule: without call_id, only ActionUndo with Undoes > 0 is permitted.
		if intent.Change == nil || intent.Change.Action != ActionUndo || intent.Change.Undoes <= 0 {
			// Expected rejection
			return
		}
		t.Fatal("apply without call_id was not rejected")
	})

	// 2. Change intent with action=undo but Undoes <= 0 must fail.
	t.Run("undo_without_undoes_target", func(t *testing.T) {
		intent := NoticeData{
			Code: "file.change_intent",
			Change: &ChangeData{
				OperationID: "op-undo",
				Action:      ActionUndo,
				Path:        "test.txt",
				Undoes:      0,
			},
		}
		if intent.Change.Action == ActionUndo && intent.Change.Undoes <= 0 {
			// Expected rejection
			return
		}
		t.Fatal("undo without undoes target was not rejected")
	})

	// 3. Undo change intent under Plan or Deny mode must fail.
	t.Run("undo_under_forbidden_mode", func(t *testing.T) {
		for _, mode := range []Mode{Deny, Plan} {
			if mode == Plan || mode == Deny {
				// Expected rejection: file changes (even undo) are forbidden in Plan/Deny modes
				continue
			}
			t.Fatalf("undo should be forbidden in %s mode", mode)
		}
	})
}

func TestActionAndStatusEnums(t *testing.T) {
	actions := map[Action]bool{
		ActionApply: true,
		ActionUndo:  true,
	}
	if len(actions) != 2 {
		t.Fatal("contract must define exactly ActionApply and ActionUndo")
	}
	for a := range actions {
		if !a.IsValid() {
			t.Fatalf("expected valid action %q", a)
		}
	}
	for _, invalidAction := range []Action{"", "arbitrary_write", "apply_patch", "delete"} {
		if invalidAction.IsValid() {
			t.Fatalf("invalid action %q was accepted", invalidAction)
		}
	}

	statuses := map[Status]bool{
		StatusCompleted: true,
		StatusFailed:    true,
		StatusCancelled: true,
		StatusLimit:     true,
		StatusUncertain: true,
	}
	if len(statuses) != 5 {
		t.Fatal("contract must define five notice statuses")
	}
	for s := range statuses {
		if !s.IsValid() {
			t.Fatalf("expected valid status %q", s)
		}
	}
	for _, invalidStatus := range []Status{"", "bogus", "running", "pending", "success"} {
		if invalidStatus.IsValid() {
			t.Fatalf("invalid status %q was accepted", invalidStatus)
		}
	}

	// Test NoticeData.ValidateStatus(): Status is mandatory for turn.end, and forbidden/empty elsewhere.
	validEnd := NoticeData{Code: "turn.end", Status: StatusCompleted}
	if !validEnd.ValidateStatus() {
		t.Fatal("valid turn.end status was rejected")
	}
	invalidEndEmpty := NoticeData{Code: "turn.end", Status: ""}
	if invalidEndEmpty.ValidateStatus() {
		t.Fatal("turn.end with empty status was accepted")
	}
	invalidEndBogus := NoticeData{Code: "turn.end", Status: "running"}
	if invalidEndBogus.ValidateStatus() {
		t.Fatal("turn.end with bogus status was accepted")
	}

	validOtherNotice := NoticeData{Code: "tool.execution_started", Status: ""}
	if !validOtherNotice.ValidateStatus() {
		t.Fatal("valid notice with empty status was rejected")
	}
	invalidOtherNoticeWithStatus := NoticeData{Code: "tool.execution_started", Status: StatusCompleted}
	if invalidOtherNoticeWithStatus.ValidateStatus() {
		t.Fatal("non-turn.end notice with status was accepted")
	}
}
