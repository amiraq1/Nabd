package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nabd/internal/event"
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

// Every event type the renderer handles must be exercised by a test, or a
// render branch can rot. The fixture predates EventEdit/EventRead — those
// are covered by their own tests in internal/agent and internal/tools — so
// this list is the fixture's renderable set.
func TestFixtureCoversAllTypes(t *testing.T) {
	ev, err := Read(fixture)
	if err != nil {
		t.Fatal(err)
	}
	got := map[event.EventType]bool{}
	for _, e := range ev {
		got[e.Type] = true
	}
	all := []event.EventType{
		event.RunStart, event.UserMsg, event.TurnStart, event.TextDelta,
		event.ToolStart, event.PermAsk, event.PermReply, event.ToolEnd,
		event.Notice, event.RunError, event.Interrupted, event.TurnEnd,
		event.Compact,
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
	live := event.Live(ev)
	if len(live) != 12 {
		t.Fatalf("got %d live events, want 12", len(live))
	}
	if live[0].Type != event.Compact {
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
	in := []event.Event{
		{Seq: 1, Time: base, Type: event.RunStart, Text: "start"},
		{Seq: 2, Parent: 1, Time: base.Add(time.Second), Type: event.UserMsg, Text: "مرحبا"},
		{Seq: 3, Parent: 2, Time: base.Add(2 * time.Second), Type: event.PermReply, Decision: event.AllowOnce},
		{Seq: 4, Parent: 3, Time: base.Add(3 * time.Second), Type: event.ToolEnd,
			Call: &event.ToolCall{ID: "t1", Name: "bash", OK: true, Exit: 0, MS: 12}},
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
	b, err := json.Marshal(event.Event{Seq: 1, Type: event.PermReply, Decision: event.Deny})
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
		var e event.Event
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if e.Decision != event.Deny {
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
	long := strings.Repeat("ب", 10000) // 20000 bytes > 16384, boundary lands mid-rune
	e := event.Event{Seq: 1, Type: event.ToolEnd,
		Call: &event.ToolCall{ID: "t1", Name: "bash", Output: long}}

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

	short := event.Event{Seq: 2, Type: event.ToolEnd,
		Call: &event.ToolCall{ID: "t2", Name: "bash", Output: "ok"}}
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

// TestToolResultTailPreservedOnDisk asserts that a tool_result with size between 4096 and 16384 bytes
// retains its lines_read, total_lines, and next_offset pagination tail in the persisted JSONL record.
func TestToolResultTailPreservedOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	j, err := NewJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	// A ~6000-byte read_file output ending with a pagination tail
	body := strings.Repeat("a", 5800)
	tail := "\n[TRUNCATED: read lines 1-27 of 193; continue with offset=28]\nlines_read=27  total_lines=193  next_offset=28\n"
	fullOutput := body + tail

	ev := event.Event{
		Seq:  1,
		Type: event.ToolEnd,
		Call: &event.ToolCall{
			ID:     "t1",
			Name:   "read_file",
			OK:     true,
			Output: fullOutput,
		},
	}

	if err := j.Append(ev); err != nil {
		t.Fatal(err)
	}

	// Read raw JSONL from disk to inspect the persisted evidence
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	t.Logf("PERSISTED_JSONL_TAIL: %s", content[len(content)-120:])

	if !strings.Contains(content, "lines_read=27") {
		t.Fatalf("persisted tool_result lost lines_read tail: len=%d MaxPersistedOutput=%d",
			len(fullOutput), event.MaxPersistedOutput)
	}
	if !strings.Contains(content, "total_lines=193") {
		t.Fatalf("persisted tool_result lost total_lines tail")
	}
	if !strings.Contains(content, "next_offset=28") {
		t.Fatalf("persisted tool_result lost next_offset tail")
	}
}

// TestEncodedBytesCalculatedPreForStore verifies that in-memory history used for network request
// encoding retains full unclipped tool output (pre-ForStore), proving that encoded_bytes is
// computed from the true network payload rather than disk-clipped persistence.
func TestEncodedBytesCalculatedPreForStore(t *testing.T) {
	longOutput := strings.Repeat("x", 20000)
	ev := event.Event{
		Seq:  1,
		Type: event.ToolEnd,
		Call: &event.ToolCall{ID: "t1", Name: "read_file", OK: true, Output: longOutput},
	}

	// Disk persistence clips via ForStore()
	stored := ev.ForStore()
	if len(stored.Call.Output) >= len(longOutput) {
		t.Fatal("expected ForStore to clip oversized output")
	}

	// But in-memory event is completely unclipped
	if len(ev.Call.Output) != 20000 {
		t.Fatalf("in-memory event output was mutated: %d", len(ev.Call.Output))
	}
}

func writeTestJournal(t *testing.T, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sess.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func marshalLine(t *testing.T, e event.Event) string {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// branchedFixture builds a journal with a compaction and a rewind, so the
// live branch is a strict subset of the file.
func branchedFixture(t *testing.T) (string, []event.Event) {
	t.Helper()
	evs := []event.Event{
		{Seq: 1, Parent: 0, Type: event.UserMsg, Text: "q1"},
		{Seq: 2, Parent: 1, Type: event.TextDelta, Text: "a1"},
		{Seq: 3, Parent: 2, Type: event.UserMsg, Text: "q2"},
		{Seq: 4, Parent: 3, Type: event.TextDelta, Text: "a2"},
		{Seq: 5, Parent: 4, Type: event.Compact, FirstKept: 3, Text: "summary"},
		{Seq: 6, Parent: 5, Type: event.UserMsg, Text: "q3"},
		{Seq: 7, Parent: 6, Type: event.TextDelta, Text: "a3"},
		// Rewind the last turn: seq 8's parent points back before seq 6,
		// making seqs 6-7 unreachable to Live().
		{Seq: 8, Parent: 5, Type: event.Rewind, Text: "rewound 1 turns"},
		{Seq: 9, Parent: 8, Type: event.UserMsg, Text: "q3 retry"},
		{Seq: 10, Parent: 9, Type: event.TextDelta, Text: "a3 retry"},
	}
	lines := make([]string, 0, len(evs))
	for _, e := range evs {
		lines = append(lines, marshalLine(t, e))
	}
	return writeTestJournal(t, lines), evs
}

func TestReadLiveBranchMatchesLive(t *testing.T) {
	path, evs := branchedFixture(t)
	live, raw, total, err := ReadLiveBranch(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if raw != nil {
		t.Fatal("keepRaw=false must not return raw lines")
	}
	if total != len(evs) {
		t.Fatalf("total=%d, want %d", total, len(evs))
	}
	want := event.Live(evs)
	if len(live) != len(want) {
		t.Fatalf("live=%d events, want %d", len(live), len(want))
	}
	for i := range want {
		if live[i].Seq != want[i].Seq {
			t.Fatalf("live[%d].Seq=%d, want %d", i, live[i].Seq, want[i].Seq)
		}
	}
	// The rewound turn (6,7) must be excluded, the pre-compact q1 (1,2) too.
	for _, e := range live {
		if e.Seq == 1 || e.Seq == 2 || e.Seq == 6 || e.Seq == 7 {
			t.Fatalf("unreachable event seq=%d in live branch", e.Seq)
		}
	}
}

func TestReadLiveBranchRawPreservesBytes(t *testing.T) {
	path, evs := branchedFixture(t)
	// Inject an unknown field into one live line: rotation must preserve it.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	var tmp []string
	for _, ln := range lines {
		var e event.Event
		if err := json.Unmarshal([]byte(ln), &e); err != nil {
			t.Fatal(err)
		}
		if e.Seq == 9 {
			ln = strings.TrimSuffix(ln, "}") + `,"future_field":"keepme"}`
		}
		tmp = append(tmp, ln)
	}
	path = writeTestJournal(t, tmp)

	live, raw, total, err := ReadLiveBranch(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if total != len(evs) {
		t.Fatalf("total=%d, want %d", total, len(evs))
	}
	if len(raw) != len(live) {
		t.Fatalf("raw lines=%d, live events=%d", len(raw), len(live))
	}
	for i, e := range live {
		var re event.Event
		if err := json.Unmarshal(raw[i], &re); err != nil {
			t.Fatalf("raw[%d] does not parse: %v", i, err)
		}
		if re.Seq != e.Seq {
			t.Fatalf("raw[%d].Seq=%d, want %d", i, re.Seq, e.Seq)
		}
		if e.Seq == 9 && !strings.Contains(string(raw[i]), `"future_field":"keepme"`) {
			t.Fatal("rotation raw lines must preserve unknown fields")
		}
		if !strings.HasSuffix(string(raw[i]), "\n") {
			t.Fatalf("raw[%d] missing trailing newline", i)
		}
	}
}

func TestReadLiveBranchNoCompactFallsBack(t *testing.T) {
	evs := []event.Event{
		{Seq: 1, Parent: 0, Type: event.UserMsg, Text: "q1"},
		{Seq: 2, Parent: 1, Type: event.TextDelta, Text: "a1"},
	}
	var lines []string
	for _, e := range evs {
		lines = append(lines, marshalLine(t, e))
	}
	path := writeTestJournal(t, lines)
	live, _, total, err := ReadLiveBranch(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(live) != 2 {
		t.Fatalf("total=%d live=%d, want 2/2", total, len(live))
	}
}

func TestScanStreamsEvents(t *testing.T) {
	path, evs := branchedFixture(t)
	var count, seqSum int
	err := Scan(path, func(e event.Event) error {
		count++
		seqSum += e.Seq
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != len(evs) {
		t.Fatalf("scanned %d events, want %d", count, len(evs))
	}
	wantSum := 0
	for _, e := range evs {
		wantSum += e.Seq
	}
	if seqSum != wantSum {
		t.Fatalf("seq sum=%d, want %d", seqSum, wantSum)
	}
}
