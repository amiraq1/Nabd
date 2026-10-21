package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"nabd/internal/event"
	"nabd/internal/provider"
)

// sessionAskingHuman is the human who always answers "allow for this session":
// the strongest raw decision the permission modal can produce, and the one that
// must not survive on a protected path.
type sessionAskingHuman struct{ calls int }

func (h *sessionAskingHuman) Ask(context.Context, event.ToolCall) event.Decision {
	h.calls++
	return event.AllowSession
}

// TestExecSensitiveWritesAlwaysAsk is the V15 behavioural pin: a write to a
// deferred-execution path (a build file, a hook, a CI workflow, a repository
// attribute file) must stop for approval on EVERY call, even while a standing
// session grant for the tool is recorded and YOLO is on. Both bypasses are
// active at once here, because either one alone would have been enough to let
// the write through before this change.
func TestExecSensitiveWritesAlwaysAsk(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args string
	}{
		{"Makefile", "write_file", `{"path":"Makefile","content":"all:\n\techo hi\n"}`},
		{"nested Makefile", "write_file", `{"path":"build/Makefile","content":"x"}`},
		{"case variant of Makefile", "write_file", `{"path":"MAKEFILE","content":"x"}`},
		{"package.json", "write_file", `{"path":"package.json","content":"{}"}`},
		{"edit package.json", "edit_file", `{"path":"package.json","old":"a","new":"b"}`},
		{"CI workflow", "write_file", `{"path":".github/workflows/ci.yml","content":"x"}`},
		{"nested .envrc", "write_file", `{"path":"sub/.envrc","content":"x"}`},
		{".gitattributes", "write_file", `{"path":".gitattributes","content":"x"}`},
		{".gitmodules", "write_file", `{"path":"vendor/.gitmodules","content":"x"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			t.Chdir(proj)

			raw := json.RawMessage(tc.args)
			prov := &scriptedProvider{turns: []scriptTurn{
				{call: &provider.ToolCall{ID: "c1", Name: tc.tool, Input: raw}},
				{call: &provider.ToolCall{ID: "c2", Name: tc.tool, Input: raw}},
				{text: "done"},
			}}

			sess, err := newInteractiveSession(prov, true)
			if err != nil {
				t.Fatalf("newInteractiveSession failed: %v", err)
			}
			// Both bypasses at once: the standing grant the policy would
			// honour for a Mutating tool, and YOLO.
			sess.pol.Record(tc.tool, event.AllowSession)
			sess.pol.SetYOLO(true)

			human := &sessionAskingHuman{}
			sess.loop.Human = human

			var asks []*event.ToolCall
			var replies []event.Event
			sess.loop.Sink = yoloSink(func(e event.Event) error {
				switch e.Type {
				case event.PermAsk:
					if e.Call != nil {
						cp := *e.Call
						asks = append(asks, &cp)
					}
				case event.PermReply:
					replies = append(replies, e)
				}
				return nil
			})

			if err := sess.loop.Run(context.Background(), "touch the build files"); err != nil {
				t.Fatalf("loop.Run failed: %v", err)
			}

			if len(asks) != 2 {
				t.Fatalf("%s on a protected path must stop for approval on every call, even with a standing grant and YOLO: got %d PermAsk events (the write ran without being asked)", tc.name, len(asks))
			}
			for i, a := range asks {
				if a.Name != tc.tool {
					t.Errorf("ask %d: tool = %q, want %q", i+1, a.Name, tc.tool)
				}
				if a.SessionGrantAllowed {
					t.Errorf("ask %d: SessionGrantAllowed = true for %s; a standing grant must not be offered for this path", i+1, tc.tool)
				}
			}
			for _, r := range replies {
				if r.Call == nil || r.Call.Name != tc.tool {
					continue
				}
				if r.Decision == event.AllowSession {
					t.Errorf("reply for %s recorded AllowSession; a protected path may only ever be approved once", tc.tool)
				}
			}
		})
	}
}

// TestInternalRepositoryWritesAreRefused pins the other half of V15: internal
// repository state (.git, .ag) is refused outright rather than asked about, and
// no bypass reaches it. YOLO and a standing grant are active, which is exactly
// how those writes used to land.
func TestInternalRepositoryWritesAreRefused(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args string
		path string
	}{
		{"git hook", "write_file", `{"path":".git/hooks/pre-commit","content":"#!/bin/sh\n"}`, ".git/hooks/pre-commit"},
		{"git config", "edit_file", `{"path":".git/config","old":"a","new":"b"}`, ""},
		{"shadow store", "write_file", `{"path":".ag/shadow/evil","content":"x"}`, ".ag/shadow/evil"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			t.Chdir(proj)

			prov := &scriptedProvider{turns: []scriptTurn{
				{call: &provider.ToolCall{ID: "c1", Name: tc.tool, Input: json.RawMessage(tc.args)}},
				{text: "done"},
			}}

			sess, err := newInteractiveSession(prov, true)
			if err != nil {
				t.Fatalf("newInteractiveSession failed: %v", err)
			}
			sess.pol.Record(tc.tool, event.AllowSession)
			sess.pol.SetYOLO(true)
			sess.loop.Human = &sessionAskingHuman{}

			var asked, denied bool
			sess.loop.Sink = yoloSink(func(e event.Event) error {
				switch e.Type {
				case event.PermAsk:
					if e.Call != nil && e.Call.Name == tc.tool {
						asked = true
					}
				case event.PermReply:
					if e.Call != nil && e.Call.Name == tc.tool && e.Decision == event.Deny {
						denied = true
					}
				}
				return nil
			})

			if err := sess.loop.Run(context.Background(), "edit repository internals"); err != nil {
				t.Fatalf("loop.Run failed: %v", err)
			}

			if asked {
				t.Errorf("%s must be refused, not asked about", tc.name)
			}
			if !denied {
				t.Fatalf("%s was not denied: internal repository state must be refused in every mode, grant or YOLO", tc.name)
			}
			if tc.path != "" {
				if _, err := os.Stat(tc.path); !os.IsNotExist(err) {
					t.Fatalf("%s was created despite the refusal", tc.path)
				}
			}
		})
	}
}
