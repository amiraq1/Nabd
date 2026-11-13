package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenDoesNotWrite(t *testing.T) {
	dirRoot := t.TempDir()
	root, err := os.OpenRoot(dirRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })

	if _, err := Open(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dirRoot, ".nabd")); !os.IsNotExist(err) {
		t.Fatalf("Open created .nabd eagerly (err=%v)", err)
	}
}

func TestWriteAppendsJSONLines(t *testing.T) {
	dirRoot := t.TempDir()
	root, err := os.OpenRoot(dirRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })

	log, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	log.now = func() time.Time { return now }

	events := []Event{
		{Event: "requested", Tool: "edit_file", ArgsSHA256: "a", Detail: "main.go"},
		{Event: "approved", Tool: "edit_file", ArgsSHA256: "a"},
		{Event: "applied", Tool: "edit_file", ArgsSHA256: "a", ResultSHA256: "b"},
	}
	for _, event := range events {
		if err := log.Write(event); err != nil {
			t.Fatal(err)
		}
	}

	file, err := os.Open(filepath.Join(dirRoot, ".nabd", "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var got []Event
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("invalid JSON line %q: %v", scanner.Text(), err)
		}
		got = append(got, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(events) {
		t.Fatalf("got %d events, want %d", len(got), len(events))
	}
	for i, event := range got {
		if event.Event != events[i].Event || event.Tool != events[i].Tool {
			t.Errorf("event %d = %+v, want %+v", i, event, events[i])
		}
		if !event.Time.Equal(now) {
			t.Errorf("event %d time = %v, want %v", i, event.Time, now)
		}
	}
}
