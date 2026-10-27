package journal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T, opts Options) (Store, string) {
	t.Helper()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "session.jsonl")
	opts.Path = path
	s, err := OpenStore(opts)
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
	})
	return s, path
}

func sampleEvent(seq, parent int64, kind Kind) Event {
	return Event{
		Seq:    seq,
		Parent: parent,
		Time:   time.Date(2026, 10, 27, 20, 0, 0, 0, time.UTC),
		Kind:   kind,
		Data:   json.RawMessage(`{"test":true}`),
	}
}

func TestAppendAndReadRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, Options{SyncMode: SyncAlways})

	e1 := Event{
		Seq:    1,
		Parent: 0,
		Time:   time.Date(2026, 10, 27, 20, 0, 0, 0, time.UTC),
		Kind:   SessionStart,
		Data:   json.RawMessage(`{"journal":1,"session_id":"s1","permission_mode":"ask"}`),
	}
	e2 := Event{
		Seq:    2,
		Parent: 1,
		Time:   time.Date(2026, 10, 27, 20, 0, 1, 0, time.UTC),
		Kind:   User,
		Data:   json.RawMessage(`{"text":"hello"}`),
	}

	seq1, err := s.Append(ctx, e1)
	if err != nil || seq1 != 1 {
		t.Fatalf("Append e1: seq=%d, err=%v", seq1, err)
	}

	seq2, err := s.Append(ctx, e2)
	if err != nil || seq2 != 2 {
		t.Fatalf("Append e2: seq=%d, err=%v", seq2, err)
	}

	events, err := s.ReadAll(ctx)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Seq != 1 || events[1].Seq != 2 {
		t.Fatalf("unexpected event seqs: %d, %d", events[0].Seq, events[1].Seq)
	}
}

func TestTruncatedTailIgnored(t *testing.T) {
	ctx := context.Background()
	s, path := newTestStore(t, Options{SyncMode: SyncAlways})

	e1 := Event{
		Seq:    1,
		Parent: 0,
		Time:   time.Date(2026, 10, 27, 20, 0, 0, 0, time.UTC),
		Kind:   SessionStart,
		Data:   json.RawMessage(`{"journal":1,"session_id":"s1","permission_mode":"ask"}`),
	}
	if _, err := s.Append(ctx, e1); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	// Append raw partial line without terminating newline to simulate incomplete write / crash
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"seq":2,"parent":1,"time":"2026-10-27T20:00:00Z","kin`); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	// Reopen store — truncated tail must be ignored cleanly
	s2, err := OpenStore(Options{Path: path, SyncMode: SyncAlways})
	if err != nil {
		t.Fatalf("reopening store with truncated tail failed: %v", err)
	}
	defer s2.Close()

	events, err := s2.ReadAll(ctx)
	if err != nil {
		t.Fatalf("ReadAll failed on truncated tail: %v", err)
	}
	if len(events) != 1 || events[0].Seq != 1 {
		t.Fatalf("expected 1 valid event, got %d", len(events))
	}
	if s2.LastSeq() != 1 {
		t.Fatalf("expected LastSeq=1, got %d", s2.LastSeq())
	}
}

func TestCorruptionInMiddleFails(t *testing.T) {
	ctx := context.Background()
	s, path := newTestStore(t, Options{SyncMode: SyncAlways})

	e1 := Event{
		Seq:    1,
		Parent: 0,
		Time:   time.Date(2026, 10, 27, 20, 0, 0, 0, time.UTC),
		Kind:   SessionStart,
		Data:   json.RawMessage(`{"journal":1,"session_id":"s1","permission_mode":"ask"}`),
	}
	e2 := Event{
		Seq:    2,
		Parent: 1,
		Time:   time.Date(2026, 10, 27, 20, 0, 1, 0, time.UTC),
		Kind:   User,
		Data:   json.RawMessage(`{"text":"line2"}`),
	}
	e3 := Event{
		Seq:    3,
		Parent: 2,
		Time:   time.Date(2026, 10, 27, 20, 0, 2, 0, time.UTC),
		Kind:   Text,
		Data:   json.RawMessage(`{"text":"line3","turn_id":"t1"}`),
	}
	_, _ = s.Append(ctx, e1)
	_, _ = s.Append(ctx, e2)
	_, _ = s.Append(ctx, e3)
	_ = s.Close()

	// Corrupt middle line (line 2)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	corrupted := []byte(strings.Replace(string(content), `"line2"`, `CORRUPTED_JSON`, 1))
	if err := os.WriteFile(path, corrupted, 0644); err != nil {
		t.Fatal(err)
	}

	// Reopen must reject corruption in the middle
	_, err = OpenStore(Options{Path: path, SyncMode: SyncAlways})
	if err == nil || !errors.Is(err, ErrCorruptedRecord) {
		t.Fatalf("expected ErrCorruptedRecord, got %v", err)
	}
}

func TestFailInjectBeforeWrite(t *testing.T) {
	ctx := context.Background()
	injectedErr := errors.New("disk full simulation")
	failInject := func(op string) error {
		if op == "before_write" {
			return injectedErr
		}
		return nil
	}

	s, _ := newTestStore(t, Options{
		SyncMode:   SyncAlways,
		FailInject: failInject,
	})

	e1 := Event{
		Seq:    1,
		Parent: 0,
		Time:   time.Date(2026, 10, 27, 20, 0, 0, 0, time.UTC),
		Kind:   SessionStart,
		Data:   json.RawMessage(`{"journal":1,"session_id":"s1","permission_mode":"ask"}`),
	}

	_, err := s.Append(ctx, e1)
	if !errors.Is(err, injectedErr) {
		t.Fatalf("expected injected error, got %v", err)
	}

	events, err := s.ReadAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events after write failure, got %d", len(events))
	}
}

func TestFailInjectAfterWriteBeforeSync(t *testing.T) {
	ctx := context.Background()
	injectedErr := errors.New("power loss before fsync")
	failInject := func(op string) error {
		if op == "before_sync" {
			return injectedErr
		}
		return nil
	}

	s, path := newTestStore(t, Options{
		SyncMode:   SyncAlways,
		FailInject: failInject,
	})

	e1 := Event{
		Seq:    1,
		Parent: 0,
		Time:   time.Date(2026, 10, 27, 20, 0, 0, 0, time.UTC),
		Kind:   SessionStart,
		Data:   json.RawMessage(`{"journal":1,"session_id":"s1","permission_mode":"ask"}`),
	}

	_, err := s.Append(ctx, e1)
	if !errors.Is(err, injectedErr) {
		t.Fatalf("expected injected sync error, got %v", err)
	}

	_ = s.Close()

	// Reopening should recover cleanly
	s2, err := OpenStore(Options{Path: path, SyncMode: SyncAlways})
	if err != nil {
		t.Fatalf("failed to reopen: %v", err)
	}
	defer s2.Close()
}

func TestSeqContinuesAfterReopen(t *testing.T) {
	ctx := context.Background()
	s, path := newTestStore(t, Options{SyncMode: SyncAlways})

	e1 := Event{
		Seq:    1,
		Parent: 0,
		Time:   time.Date(2026, 10, 27, 20, 0, 0, 0, time.UTC),
		Kind:   SessionStart,
		Data:   json.RawMessage(`{"journal":1,"session_id":"s1","permission_mode":"ask"}`),
	}
	_, _ = s.Append(ctx, e1)
	_ = s.Close()

	// Reopen
	s2, err := OpenStore(Options{Path: path, SyncMode: SyncAlways})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	if s2.LastSeq() != 1 {
		t.Fatalf("expected lastSeq=1, got %d", s2.LastSeq())
	}

	// Auto-assign seq on next append
	e2 := Event{
		Parent: 1,
		Time:   time.Date(2026, 10, 27, 20, 0, 1, 0, time.UTC),
		Kind:   User,
		Data:   json.RawMessage(`{"text":"next"}`),
	}
	seq2, err := s2.Append(ctx, e2)
	if err != nil || seq2 != 2 {
		t.Fatalf("expected seq 2, got %d (err: %v)", seq2, err)
	}
}

func TestConcurrentAppend(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, Options{SyncMode: SyncAlways})

	e1 := Event{
		Seq:    1,
		Parent: 0,
		Time:   time.Date(2026, 10, 27, 20, 0, 0, 0, time.UTC),
		Kind:   SessionStart,
		Data:   json.RawMessage(`{"journal":1,"session_id":"concurrent","permission_mode":"ask"}`),
	}
	if _, err := s.Append(ctx, e1); err != nil {
		t.Fatal(err)
	}

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			e := Event{
				Parent: 1,
				Time:   time.Date(2026, 10, 27, 20, 0, 0, 0, time.UTC),
				Kind:   User,
				Data:   json.RawMessage(`{"text":"ping"}`),
			}
			_, _ = s.Append(ctx, e)
		}()
	}

	wg.Wait()

	events, err := s.ReadAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != n+1 {
		t.Fatalf("expected %d events, got %d", n+1, len(events))
	}

	// Verify strictly monotonic seqs
	for i := 1; i < len(events); i++ {
		if events[i].Seq != events[i-1].Seq+1 {
			t.Fatalf("broken seq at index %d: %d followed by %d", i, events[i-1].Seq, events[i].Seq)
		}
	}
}

func TestGoldenFixturesLoad(t *testing.T) {
	ctx := context.Background()
	fixtures := []string{"read_denied", "edit_undo", "rewind"}

	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("../../testdata", name+".jsonl")
			s, err := OpenStore(Options{Path: path, SyncMode: SyncNone})
			if err != nil {
				t.Fatalf("OpenStore failed on fixture %s: %v", name, err)
			}
			defer s.Close()

			events, err := s.ReadAll(ctx)
			if err != nil {
				t.Fatalf("ReadAll failed on fixture %s: %v", name, err)
			}
			if len(events) == 0 {
				t.Fatalf("fixture %s loaded 0 events", name)
			}

			// Verify referenceLive works on loaded events
			live := referenceLive(events)
			if len(live) == 0 {
				t.Fatalf("fixture %s produced empty live state", name)
			}
		})
	}
}
