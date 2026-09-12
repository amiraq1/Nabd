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
//
// Security contract (NBD-306):
//   - New files are created with mode 0o600 (owner read/write only).
//   - Existing files that are wider than 0o600 are hardened via Fchmod before
//     any data is written. If Fchmod fails the file is closed and an error is
//     returned; we never continue with an exposed journal.
//   - A parent directory nabd has to create is created with mode 0o700, so a
//     permissive umask cannot leave the journal directory world-listable. A
//     parent that already exists is left exactly as the caller set it: nabd
//     never widens and never tightens a directory it did not create. Callers
//     that own the session directory (the nabd default ~/.ag/sessions path)
//     additionally pin it to 0o700 themselves; see ensureDefaultSessionDir
//     and defaultSessionDir in cmd/ag.
func NewJSONL(path string) (*JSONL, error) {
	if err := ensurePrivateParent(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	// Harden an existing file that was created wider than 0o600.
	// Fchmod operates on the open file descriptor, so there is no TOCTOU
	// window between the mode check and the chmod itself.
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return nil, fmt.Errorf("store: harden journal permissions: %w", err)
	}
	return &JSONL{path: path, f: f, w: bufio.NewWriter(f)}, nil
}

// NewJSONLExclusive creates a new journal without ever opening an existing
// file. Callers use a fresh candidate name and retry on os.ErrExist.
func NewJSONLExclusive(path string) (*JSONL, error) {
	if err := ensurePrivateParent(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("store: harden new journal permissions: %w", err)
	}
	return &JSONL{path: path, f: f, w: bufio.NewWriter(f)}, nil
}

// ensurePrivateParent creates dir with mode 0o700 if it does not exist, and
// leaves an existing directory untouched — not even to narrow it, because a
// caller-supplied --dir belongs to the caller.
//
// The mode contract for the created case is:
//   - dir (the last element) ends up exactly 0o700, because MkdirAll's mode
//     argument is masked by the umask and the explicit Chmod is not;
//   - every ancestor MkdirAll has to create on the way is private too (no
//     group or other bits), since MkdirAll applies the same 0o700 mode to all
//     of them. An ancestor can therefore be narrower than 0o700 under an
//     unusual umask, but never wider.
func ensurePrivateParent(dir string) error {
	switch _, err := os.Stat(dir); {
	case err == nil:
		return nil
	case !os.IsNotExist(err):
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
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
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var e agent.Event
		if err := json.Unmarshal(raw, &e); err != nil {
			// Tolerate a truncated final line only if nothing follows it.
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
	return out, nil
}
