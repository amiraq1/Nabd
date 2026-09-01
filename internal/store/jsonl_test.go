package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nabd/internal/agent"
)

const fixture = "../../testdata/session.jsonl"

func TestReadFixture(t *testing.T) {
	ev, err := Read(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != 18 {
		t.Fatalf("got %d events, want 18", len(ev))
	}

	seen := map[int]bool{0: true}
	for i, e := range ev {
		if e.Seq <= 0 {
			t.Errorf("line %d: seq %d not positive", i+1, e.Seq)
		}
		if i > 0 && e.Seq <= ev[i-1].Seq {
			t.Errorf("line %d: seq went backwards", i+1)
		}
		if i > 0 && e.Time.Before(ev[i-1].Time) {
			t.Errorf("line %d: time travel", i+1)
		}
		if !seen[e.Parent] {
			t.Errorf("line %d: parent %d unseen", i+1, e.Parent)
		}
		if e.Type == "" {
			t.Errorf("line %d: empty type", i+1)
		}
		seen[e.Seq] = true
	}
}

// Every event type must appear at least once, or render.go grows branches
// that no test ever walks.
func TestFixtureCoversAllTypes(t *testing.T) {
	ev, err := Read(fixture)
	if err != nil {
		t.Fatal(err)
	}
	got := map[agent.EventType]bool{}
	for _, e := range ev {
		got[e.Type] = true
	}
	all := []agent.EventType{
		agent.RunStart, agent.UserMsg, agent.TurnStart, agent.TextDelta,
		agent.ToolStart, agent.PermAsk, agent.PermReply, agent.ToolEnd,
		agent.Notice, agent.RunError, agent.Interrupted, agent.TurnEnd,
		agent.RunEnd, agent.Compact,
	}
	for _, ty := range all {
		if !got[ty] {
			t.Errorf("fixture never exercises %q", ty)
		}
	}
}

// The compaction entry at seq 10 keeps from seq 7 onward: summary + 7,8,9
// + 11..18 = 12 events, and the summary must lead.
func TestLiveHonoursCompaction(t *testing.T) {
	ev, err := Read(fixture)
	if err != nil {
		t.Fatal(err)
	}
	live := agent.Live(ev)
	if len(live) != 12 {
		t.Fatalf("got %d live events, want 12", len(live))
	}
	if live[0].Type != agent.Compact {
		t.Errorf("live[0] is %q, want compact summary first", live[0].Type)
	}
	for _, e := range live[1:] {
		if e.Seq < 7 {
			t.Errorf("seq %d survived compaction, first_kept is 7", e.Seq)
		}
	}
}

func TestAppendRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "s.jsonl")
	j, err := NewJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	if j.Path() != path {
		t.Errorf("Path() = %q", j.Path())
	}

	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	in := []agent.Event{
		{Seq: 1, Time: base, Type: agent.RunStart, Text: "start"},
		{Seq: 2, Parent: 1, Time: base.Add(time.Second), Type: agent.UserMsg, Text: "مرحبا"},
		{Seq: 3, Parent: 2, Time: base.Add(2 * time.Second), Type: agent.PermReply, Decision: agent.AllowOnce},
		{Seq: 4, Parent: 3, Time: base.Add(3 * time.Second), Type: agent.ToolEnd,
			Call: &agent.ToolCall{ID: "t1", Name: "bash", OK: true, Exit: 0, MS: 12}},
	}
	for _, e := range in {
		if err := j.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}

	out, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("got %d back, wrote %d", len(out), len(in))
	}
	// Compare as JSON: time.Time carries a monotonic reading and a
	// location pointer that never survive a round trip, and neither
	// belongs in the journal's identity.
	for i := range in {
		want, _ := json.Marshal(in[i].ForStore())
		got, _ := json.Marshal(out[i])
		if string(want) != string(got) {
			t.Errorf("event %d:\n want %s\n got  %s", i+1, want, got)
		}
	}
}

// Deny is the zero value, so it is never written; anything unrecognised
// must read back as Deny.
func TestDecisionFailsClosed(t *testing.T) {
	b, err := json.Marshal(agent.Event{Seq: 1, Type: agent.PermReply, Decision: agent.Deny})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "decision") {
		t.Errorf("deny should be omitted: %s", b)
	}

	for _, raw := range []string{
		`{"seq":1,"type":"perm_reply"}`,
		`{"seq":1,"type":"perm_reply","decision":"bogus"}`,
		`{"seq":1,"type":"perm_reply","decision":""}`,
		`{"seq":1,"type":"perm_reply","decision":"ALLOW"}`,
	} {
		var e agent.Event
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if e.Decision != agent.Deny {
			t.Errorf("%s decoded to %v, want deny", raw, e.Decision)
		}
	}
}

// A crash mid-Append leaves a partial final line. That is recoverable.
// A corrupt line in the middle is not, and must not be swallowed.
func TestReadTolerance(t *testing.T) {
	dir := t.TempDir()

	torn := filepath.Join(dir, "torn.jsonl")
	body := `{"seq":1,"t":"2026-09-01T10:00:00Z","type":"run_start"}` + "\n" +
		"\n" + // blank line, skipped
		`{"seq":2,"t":"2026-09-01T10:00:01Z","type":"run_e`
	if err := os.WriteFile(torn, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ev, err := Read(torn)
	if err != nil {
		t.Fatalf("torn final line should be tolerated: %v", err)
	}
	if len(ev) != 1 {
		t.Errorf("got %d events, want 1", len(ev))
	}

	mid := filepath.Join(dir, "mid.jsonl")
	body = `{"seq":1,"t":"2026-09-01T10:00:00Z","type":"run_start"}` + "\n" +
		"{ this is not json\n" +
		`{"seq":3,"t":"2026-09-01T10:00:02Z","type":"run_end"}` + "\n"
	if err := os.WriteFile(mid, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(mid); err == nil {
		t.Error("corrupt middle line must be an error")
	}

	if _, err := Read(filepath.Join(dir, "nope.jsonl")); err == nil {
		t.Error("missing file must be an error")
	}
}

// Output is capped on disk but must stay valid UTF-8 and stay marked.
func TestForStoreTruncatesOnRuneBoundary(t *testing.T) {
	long := strings.Repeat("ب", 4000) // 8000 bytes, boundary lands mid-rune
	e := agent.Event{Seq: 1, Type: agent.ToolEnd,
		Call: &agent.ToolCall{ID: "t1", Name: "bash", Output: long}}

	s := e.ForStore()
	if s.Call.Output == long {
		t.Fatal("output was not truncated")
	}
	if !strings.Contains(s.Call.Output, "truncated") {
		t.Error("truncation not marked")
	}
	if !utf8ValidString(s.Call.Output) {
		t.Error("truncation split a rune")
	}
	if e.Call.Output != long {
		t.Error("ForStore mutated the original event")
	}

	short := agent.Event{Seq: 2, Type: agent.ToolEnd,
		Call: &agent.ToolCall{ID: "t2", Name: "bash", Output: "ok"}}
	if short.ForStore().Call.Output != "ok" {
		t.Error("small output must pass through untouched")
	}
}

func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}
