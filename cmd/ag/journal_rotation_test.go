package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/event"
	"nabd/internal/store"
)

// oversizedJournalFixture writes a journal just over store.MaxJournalBytes
// whose live branch is a small tail after a Compact event.
func oversizedJournalFixture(t *testing.T) (path string, liveSeqs []int) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "session-test.jsonl")

	var lines []string
	seq := 0
	emit := func(e event.Event) {
		seq++
		e.Seq = seq
		if e.Parent == 0 {
			e.Parent = seq - 1
		}
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	pad := strings.Repeat("x", 16*1024) // 16 KiB per event, like MaxPersistedOutput
	for i := 0; i < 1500; i++ {
		emit(event.Event{Type: event.UserMsg, Text: pad})
	}
	firstKept := seq + 1
	emit(event.Event{Type: event.Compact, FirstKept: firstKept, Text: "summary"})
	for i := 0; i < 600; i++ {
		emit(event.Event{Type: event.UserMsg, Text: pad})
		liveSeqs = append(liveSeqs, seq)
	}
	// Sanity: the fixture really is oversized.
	if got := int64(len(strings.Join(lines, "\n")) + 1); got <= store.MaxJournalBytes {
		t.Fatalf("fixture is %d bytes, not over MaxJournalBytes", got)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, liveSeqs
}

func TestMaybeRotateOversizedJournal(t *testing.T) {
	path, liveSeqs := oversizedJournalFixture(t)

	origEvents, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}

	rotated, err := maybeRotateOversizedJournal(path, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !rotated {
		t.Fatal("expected rotation of oversized journal")
	}

	// The archive holds the full history under a non-.jsonl name.
	archives, err := filepath.Glob(path + ".archived-*")
	if err != nil || len(archives) != 1 {
		t.Fatalf("archives=%v err=%v", archives, err)
	}
	if strings.HasSuffix(archives[0], ".jsonl") {
		t.Fatalf("archive %q must not look like a session journal", archives[0])
	}
	archived, err := store.Read(archives[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != len(origEvents) {
		t.Fatalf("archive has %d events, want %d", len(archived), len(origEvents))
	}

	// The live file now contains only the live branch: compact + tail.
	kept, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != len(liveSeqs)+1 {
		t.Fatalf("rotated journal has %d events, want %d (compact + live tail)", len(kept), len(liveSeqs)+1)
	}
	if kept[0].Type != event.Compact {
		t.Fatalf("rotated journal must start with the Compact event, got %v", kept[0].Type)
	}
	for i, e := range kept[1:] {
		if e.Seq != liveSeqs[i] {
			t.Fatalf("kept[%d].Seq=%d, want %d", i+1, e.Seq, liveSeqs[i])
		}
	}
	if got := len(kept); event.Live(kept)[0].Type != event.Compact || got != len(event.Live(origEvents)) {
		t.Fatal("rotated file's live branch differs from the original's")
	}

	// Rotation is idempotent: the small file is left alone.
	rotated, err = maybeRotateOversizedJournal(path, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if rotated {
		t.Fatal("second rotation must be a no-op")
	}

	// The rotated journal stays writable for --continue.
	j, err := store.NewJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Append(event.Event{Type: event.UserMsg, Text: "after rotation"}); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(kept)+1 || after[len(after)-1].Text != "after rotation" {
		t.Fatal("append after rotation failed")
	}
}

func TestMaybeRotateOversizedJournalSkipsSmall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rotated, err := maybeRotateOversizedJournal(path, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if rotated {
		t.Fatal("small journal must not rotate")
	}
	if _, err := os.Stat(path + ".archived-1"); !os.IsNotExist(err) {
		t.Fatal("no archive should exist")
	}
}
