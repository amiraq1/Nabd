package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/agent"
)

// The tests here drive the whole loop (agent.Loop with a real Registry), not
// Registry.RunDetailed, because that is the path a model's call actually
// travels: the loop repairs, then classifies, then dispatches. A test at the
// registry boundary alone cannot tell whether the repair layer has already
// normalised the payload by the time the argument decoder sees it.

// ranFile is the side effect every case in this file would produce if the
// command were allowed to run. Decoding happens before exec, so it appears only
// where the call was accepted.
const ranFile = "ran.txt"

// TestBashUnknownFieldIsDroppedAndRuns is the round the model no longer loses: a
// bash call carrying a key the tool does not declare used to reach the strict
// decoder and fail as a whole. The repair layer removes the key before
// classification — and states the loss on the ToolStart event as data, so the
// executed arguments being shorter than the model's is visible rather than
// implied — and the command runs.
func TestBashUnknownFieldIsDroppedAndRuns(t *testing.T) {
	events, dir := runBashCall(t, agent.VerdictAllow, &bashProvider{
		raw: json.RawMessage(`{"cmd":"touch ` + ranFile + `","bogus":1}`),
	})

	start := lastToolStart(t, events)
	if len(start.DroppedArgs) != 1 || start.DroppedArgs[0] != "bogus" {
		t.Fatalf("ToolStart reported dropped keys %v, want [bogus]", start.DroppedArgs)
	}
	if strings.Contains(string(start.Args), "bogus") {
		t.Fatalf("dropped key is still in the arguments that ran: %s", start.Args)
	}

	end := lastToolEnd(t, events)
	if !end.OK {
		t.Fatalf("call whose stray key was dropped did not run: %q", end.Output)
	}
	if _, err := os.Stat(filepath.Join(dir, ranFile)); err != nil {
		t.Fatalf("accepted call did not run the command: %v", err)
	}
}

// TestBashRepairedCallRunsWhenPayloadIsClean is the other half of the repair
// path: an aliased field is renamed, nothing is dropped, and the command runs.
func TestBashRepairedCallRunsWhenPayloadIsClean(t *testing.T) {
	events, _ := runBashCall(t, agent.VerdictAllow, &bashProvider{
		raw: json.RawMessage(`{"command":"echo repaired"}`),
	})

	start := lastToolStart(t, events)
	if strings.Contains(string(start.Args), `"command"`) {
		t.Fatalf("aliased field survived repair; ToolStart args=%s", start.Args)
	}
	if len(start.DroppedArgs) != 0 {
		t.Fatalf("nothing was undeclared, yet ToolStart reported drops %v", start.DroppedArgs)
	}
	end := lastToolEnd(t, events)
	if !end.OK || !strings.Contains(end.Output, "repaired") {
		t.Fatalf("repaired, well-formed call did not run: ok=%v out=%q", end.OK, end.Output)
	}
}

// TestBashStrictDecodeStillFailsThroughLoop covers the calls the repair layer
// declines, where the strict decoder stays the boundary that decides: nothing
// runs, and the model reads why. Two shapes reach it — a payload the layer will
// not rewrite, and a call needing more corrections than maxFixes, which it treats
// as a different call and returns untouched.
func TestBashStrictDecodeStillFailsThroughLoop(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			// Two values for one key. Repair normalises nothing here: choosing
			// which value the model meant is how a call silently becomes a
			// different call.
			name: "duplicate key",
			raw:  `{"cmd":"touch ` + ranFile + `","cmd":"echo no"}`,
			want: "duplicate key",
		},
		{
			// Two renames, an integer sent as text, and three undeclared keys:
			// more than the layer will correct, so it declines and the boundary
			// rejects rather than the layer guessing.
			name: "more corrections than the layer will make",
			raw:  `{"command":"touch ` + ranFile + `","timeout":"30","bogus":1,"junk2":2,"junk3":3}`,
			want: "invalid args",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events, dir := runBashCall(t, agent.VerdictAllow, &bashProvider{raw: json.RawMessage(tc.raw)})

			end := lastToolEnd(t, events)
			if end.OK {
				t.Fatalf("rejected call reported OK: %q", end.Output)
			}
			if !strings.Contains(end.Output, "invalid args") || !strings.Contains(end.Output, tc.want) {
				t.Fatalf("ToolEnd %q does not name the decode failure (%q)", end.Output, tc.want)
			}
			if _, err := os.Stat(filepath.Join(dir, ranFile)); !os.IsNotExist(err) {
				t.Fatalf("a rejected call still started a subprocess (stat error=%v)", err)
			}
		})
	}
}

// TestBashPromptShowsTheRepairedCall is the visible half of "what the user
// approved is what ran". The loop repairs, then emits ToolStart, then asks. The
// prompt event carries the repaired call — the renamed field, and without the
// dropped key — and the command that executes is that same payload, so the
// arguments on screen are the arguments that run. A prompt showing
// {"command": ...} while {"cmd": ...} executes would break the match with every
// journal field still agreeing, which is why it is pinned here rather than
// inferred from the ordering test.
func TestBashPromptShowsTheRepairedCall(t *testing.T) {
	events, dir := runBashCall(t, agent.VerdictAsk, &bashProvider{
		raw: json.RawMessage(`{"command":"touch prompt.txt","bogus":1}`),
	})

	startIdx, askIdx, ask := -1, -1, agent.ToolCall{}
	for i, e := range events {
		switch e.Type {
		case agent.ToolStart:
			startIdx = i
		case agent.PermAsk:
			askIdx, ask = i, *e.Call
		}
	}
	if startIdx < 0 || askIdx < 0 {
		t.Fatalf("missing events: start=%d ask=%d", startIdx, askIdx)
	}
	if startIdx >= askIdx {
		t.Fatalf("the prompt was shown before the call was recorded: start=%d ask=%d", startIdx, askIdx)
	}

	start := lastToolStart(t, events)
	if string(ask.Args) != string(start.Args) {
		t.Fatalf("prompt args %s differ from the args that ran %s", ask.Args, start.Args)
	}
	if strings.Contains(string(ask.Args), "command") || strings.Contains(string(ask.Args), "bogus") {
		t.Fatalf("prompt shows the model's call rather than the repaired one: %s", ask.Args)
	}
	if len(ask.DroppedArgs) != 1 || ask.DroppedArgs[0] != "bogus" {
		t.Fatalf("prompt did not disclose the dropped key: %v", ask.DroppedArgs)
	}

	if _, err := os.Stat(filepath.Join(dir, "prompt.txt")); err != nil {
		t.Fatalf("approved command did not run: %v", err)
	}
}

func lastToolStart(t *testing.T, events []agent.Event) agent.ToolCall {
	t.Helper()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == agent.ToolStart && events[i].Call != nil {
			return *events[i].Call
		}
	}
	t.Fatal("no ToolStart in the run")
	return agent.ToolCall{}
}

func lastToolEnd(t *testing.T, events []agent.Event) agent.ToolCall {
	t.Helper()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == agent.ToolEnd && events[i].Call != nil {
			return *events[i].Call
		}
	}
	t.Fatal("no ToolEnd in the run")
	return agent.ToolCall{}
}
