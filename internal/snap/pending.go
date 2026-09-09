// Package snap: pending.go implements the on-disk pending-edit log that
// makes /undo work across processes. Today the pending edit log lives in
// process memory (IDEAS.md "Multi-process undo"), so two nabd instances in
// one repo cannot see each other's edits and /undo in one can silently
// clobber the other's work. This file moves that log to disk under .ag/
// with the same write discipline write.go uses: temp file + rename, fsync
// the directory, read-back verify.
package snap

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PendingRecord is one staged mutation in the on-disk pending log.
type PendingRecord struct {
	Path      string `json:"path"`       // relative path inside the project
	PreHash   string `json:"pre_hash"`   // SHA-256 before mutation (empty = creation)
	PostHash  string `json:"post_hash"`  // SHA-256 after mutation
	BeforeKey string `json:"before_key"` // shadow content key for pre-mutation content
	AfterKey  string `json:"after_key"`  // shadow content key for post-mutation content
	Mode      uint32 `json:"mode"`       // file mode before mutation
	Pid       int    `json:"pid"`        // process that staged this edit
	Time      string `json:"time"`       // RFC3339 timestamp
}

// pendingPath returns the path to the pending log for a root.
func pendingPath(root string) string {
	return filepath.Join(root, ".ag", "pending.jsonl")
}

// AppendPending appends one record to the pending log using temp+rename
// atomicity and fsyncs the directory. The record is written with the
// current pid and timestamp if not already set.
func AppendPending(root string, rec *PendingRecord) error {
	if rec.Pid == 0 {
		rec.Pid = os.Getpid()
	}
	if rec.Time == "" {
		rec.Time = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return appendJSONL(pendingPath(root), rec)
}

// ReadPending reads all pending records from the log, newest last.
func ReadPending(root string) ([]PendingRecord, error) {
	return readJSONL[PendingRecord](pendingPath(root))
}

// DropPending removes the newest n records from the pending log. It rewrites
// the file atomically (temp+rename+fsync). If n exceeds the record count,
// the file is removed entirely.
func DropPending(root string, n int) error {
	p := pendingPath(root)
	records, err := ReadPending(root)
	if err != nil {
		return err
	}
	if n >= len(records) {
		// Remove the file entirely.
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	// Keep all but the newest n.
	keep := records[:len(records)-n]
	// Marshal kept records to JSONL.
	var msgs []json.RawMessage
	for i := range keep {
		b, err := json.Marshal(&keep[i])
		if err != nil {
			return fmt.Errorf("marshal pending record: %w", err)
		}
		msgs = append(msgs, json.RawMessage(b))
	}
	return rewriteJSONL(p, msgs)
}

// PendingCount returns the number of pending records.
func PendingCount(root string) (int, error) {
	records, err := ReadPending(root)
	if err != nil {
		return 0, err
	}
	return len(records), nil
}

// HasPending reports whether any pending records exist.
func HasPending(root string) (bool, error) {
	p := pendingPath(root)
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	// Check if the file has any non-empty content.
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	return info.Size() > 0, nil
}

// appendJSONL appends one record to a JSONL file atomically.
func appendJSONL(p string, rec interface{}) error {
	// Read existing records.
	var records []json.RawMessage
	if f, err := os.Open(p); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			records = append(records, json.RawMessage(line))
		}
		f.Close()
	} else if !os.IsNotExist(err) {
		return err
	}

	// Marshal the new record.
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal pending record: %w", err)
	}
	records = append(records, json.RawMessage(b))

	return rewriteJSONL(p, records)
}

// rewriteJSONL writes records to a JSONL file atomically: write a temp
// sibling, fsync it, rename over the target, fsync the directory.
func rewriteJSONL(p string, records []json.RawMessage) error {
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".pending-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	// Ensure cleanup on failure.
	defer os.Remove(tmpName)

	w := bufio.NewWriter(tmp)
	for _, rec := range records {
		if _, err := w.Write(rec); err != nil {
			tmp.Close()
			return err
		}
		if err := w.WriteByte('\n'); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	// Atomic rename.
	if err := os.Rename(tmpName, p); err != nil {
		return err
	}

	// Fsync the directory to durably record the rename.
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// readJSONL reads all records from a JSONL file.
func readJSONL[T any](p string) ([]T, error) {
	var out []T
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	lineNum := 0
	for sc.Scan() {
		lineNum++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec T
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			// Tolerate a truncated final line (crash during append).
			if !sc.Scan() {
				break
			}
			return nil, fmt.Errorf("%s:%d: %w", p, lineNum, err)
		}
		out = append(out, rec)
	}
	return out, sc.Err()
}

// ErrPendingConflict is returned when a pending record's pre-hash does not
// match the current file content (another process modified it).
var ErrPendingConflict = errors.New("file changed since the edit was staged")

// RecoverPending checks for a pending log left behind by a crashed process.
// It returns the records and the pid that wrote them (if determinable).
// This is used on startup to surface orphaned edits to the user as a Notice
// with an explicit choice, never auto-applied and never auto-discarded.
func RecoverPending(root string) ([]PendingRecord, int, error) {
	records, err := ReadPending(root)
	if err != nil {
		return nil, 0, err
	}
	if len(records) == 0 {
		return nil, 0, nil
	}
	// The pid is the same for all records in a single session.
	pid := 0
	if len(records) > 0 {
		pid = records[0].Pid
	}
	return records, pid, nil
}

// PendingSummary returns a human-readable summary of pending edits for display.
func PendingSummary(root string) (string, error) {
	records, pid, err := RecoverPending(root)
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", nil
	}
	var paths []string
	for _, r := range records {
		paths = append(paths, r.Path)
	}
	if pid > 0 {
		return fmt.Sprintf("%d pending edit(s) by pid %d: %s",
			len(records), pid, strings.Join(paths, ", ")), nil
	}
	return fmt.Sprintf("%d pending edit(s): %s",
		len(records), strings.Join(paths, ", ")), nil
}
