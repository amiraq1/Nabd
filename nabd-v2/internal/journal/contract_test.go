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
				seen[e.Seq] = true
				prev = e.Seq
			}
		})
	}
}

func TestGoldenCallBindings(t *testing.T) {
	for _, name := range []string{"read_denied", "edit_undo"} {
		t.Run(name, func(t *testing.T) {
			calls := map[string]CallData{}
			decisions := map[string]Decision{}
			results := map[string]bool{}
			ended := map[string]bool{}
			for _, e := range fixture(t, name) {
				switch e.Kind {
				case ToolCall:
					d := decode[CallData](t, e.Data)
					if d.CallID == "" || d.TurnID == "" || calls[d.CallID].CallID != "" {
						t.Fatal("duplicate/empty call")
					}
					switch d.Class {
					case Read, Write, Execute:
					default:
						t.Fatal("invalid class")
					}
					var args map[string]any
					if err := json.Unmarshal(d.Args, &args); err != nil || args == nil {
						t.Fatal("args must be object")
					}
					canonical, err := json.Marshal(map[string]any{"name": d.Name, "args": args, "cwd": "/project"})
					if err != nil {
						t.Fatal(err)
					}
					if hash(canonical) != d.RequestHash {
						t.Fatal("request digest mismatch")
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
						ended[d.TurnID] = true
					}
					if d.Code == "tool.execution_started" || (d.Code == "file.change_intent" && d.CallID != "") {
						if decisions[d.CallID] != AllowOnce && decisions[d.CallID] != AllowSession {
							t.Fatal("execution without consent")
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
		case "apply":
			if d.Undoes != 0 {
				t.Fatal("apply has undo target")
			}
			applied[e.Seq] = d
		case "undo":
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
