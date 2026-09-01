// Package store persists the event journal as one JSON object per line.
// Append-only: a line, once written, is never modified.
package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"nabd/internal/agent"
)

// JSONL is a single session file, safe for concurrent Append.
type JSONL struct {
	mu   sync.Mutex
	path string
	f    *os.File
	w    *bufio.Writer
}

// NewJSONL opens path for appending, creating parents if needed.
func NewJSONL(path string) (*JSONL, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &JSONL{path: path, f: f, w: bufio.NewWriter(f)}, nil
}

func (j *JSONL) Path() string { return j.path }

// Append writes one event as one line and flushes it to the kernel.
//
// The whole line is built in memory first: a partial line on disk is the
// one corruption replay cannot recover from. Sync is deliberately absent
// on the hot path -- on a phone it costs more than the crash it prevents,
// and Read already tolerates a truncated final line.
func (j *JSONL) Append(e agent.Event) error {
	b, err := json.Marshal(e.ForStore())
	if err != nil {
		return fmt.Errorf("marshal seq %d: %w", e.Seq, err)
	}
	b = append(b, '\n')

	j.mu.Lock()
	defer j.mu.Unlock()
	if _, err := j.w.Write(b); err != nil {
		return err
	}
	return j.w.Flush()
}

// Close flushes and syncs. This is the only fsync in the package.
func (j *JSONL) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return nil
	}
	err := j.w.Flush()
	if serr := j.f.Sync(); err == nil {
		err = serr
	}
	if cerr := j.f.Close(); err == nil {
		err = cerr
	}
	j.f = nil
	return err
}

// Read parses a whole session. It is deliberately forgiving: a blank line
// is skipped, and an unparsable final line is assumed to be a crash during
// Append and dropped. An unparsable line anywhere else is a real error.
func Read(path string) ([]agent.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var out []agent.Event
	var pending []byte // last decoded-and-kept raw line, for error context
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var e agent.Event
		if err := json.Unmarshal(raw, &e); err != nil {
			pending = append(pending[:0], raw...)
			// Tolerate only if this is the final line.
			if sc.Scan() {
				return nil, fmt.Errorf("%s:%d: %w", path, line, err)
			}
			break
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, err
		}
	}
	_ = pending
	return out, nil
}
