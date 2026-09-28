// Package store persists the event journal as one JSON object per line.
// Append-only: a line, once written, is never modified.
package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"nabd/internal/agent"
)

// EventRedactor returns the event representation that may be persisted.
// Implementations must not mutate the supplied event and must be safe for
// concurrent use.
type EventRedactor func(agent.Event) agent.Event

// Options controls optional JSONL persistence behavior.
// The zero value preserves the original raw-journal behavior.
type Options struct {
	Redact EventRedactor
}

// JSONL is a single session file, safe for concurrent Append.
type JSONL struct {
	mu     sync.Mutex
	path   string
	f      *os.File
	w      *bufio.Writer
	redact EventRedactor
	// needsSeparator is true when the existing file ended with a valid JSON
	// value but no newline. The first append must not concatenate two objects.
	needsSeparator bool
}

// NewJSONL opens path for appending, creating parents if needed.
//
// Security contract (NBD-306):
//   - New files are created with mode 0o600 (owner read/write only).
//   - The append target is opened with O_NOFOLLOW: a symlink at the journal
//     path fails closed instead of redirecting a continued session's appends
//     into an attacker-chosen file.
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
	return NewJSONLWithOptions(path, Options{})
}

// NewJSONLWithOptions opens path with explicit persistence options.
func NewJSONLWithOptions(path string, opts Options) (*JSONL, error) {
	if err := ensurePrivateParent(filepath.Dir(path)); err != nil {
		return nil, err
	}
	// Fail closed before touching the target: a symlink at the journal path
	// must never be followed for reading, tail recovery, or appending. The
	// opens below additionally use O_NOFOLLOW to cover a symlink planted in
	// the (tiny) window after this check.
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("store: journal path is a symlink: %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	needsSeparator, err := prepareExistingJournal(path)
	if err != nil {
		return nil, err
	}
	f, err := openAppendFile(path)
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
	return &JSONL{
		path:           path,
		f:              f,
		w:              bufio.NewWriter(f),
		redact:         opts.Redact,
		needsSeparator: needsSeparator,
	}, nil
}

// NewJSONLExclusive creates a new journal without ever opening an existing
// file. Callers use a fresh candidate name and retry on os.ErrExist.
func NewJSONLExclusive(path string) (*JSONL, error) {
	return NewJSONLExclusiveWithOptions(path, Options{})
}

// NewJSONLExclusiveWithOptions creates a new journal with explicit persistence
// options while retaining exclusive-create semantics.
func NewJSONLExclusiveWithOptions(path string, opts Options) (*JSONL, error) {
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
	return &JSONL{
		path:   path,
		f:      f,
		w:      bufio.NewWriter(f),
		redact: opts.Redact,
	}, nil
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
	persisted := e
	if j.redact != nil {
		persisted = j.redact(e)
	}

	b, err := json.Marshal(persisted.ForStore())
	if err != nil {
		return fmt.Errorf("marshal seq %d: %w", e.Seq, err)
	}
	b = append(b, '\n')

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.needsSeparator {
		if _, err := j.w.WriteString("\n"); err != nil {
			return err
		}
		j.needsSeparator = false
	}
	if _, err := j.w.Write(b); err != nil {
		return err
	}
	return j.w.Flush()
}

// prepareExistingJournal validates the existing append target before a
// continuation can write to it. A malformed final line is a crash-torn tail:
// preserve the original bytes in a private recovery copy, then truncate only
// the active file back to the last complete line. A malformed line followed by
// any later bytes is treated as corruption and refuses the append.
func prepareExistingJournal(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(data) == 0 {
		return false, nil
	}

	needsSeparator := data[len(data)-1] != '\n'
	offset := 0
	lineNo := 0
	for offset < len(data) {
		lineNo++
		relativeEnd := bytes.IndexByte(data[offset:], '\n')
		end := len(data)
		hasNewline := relativeEnd >= 0
		if hasNewline {
			end = offset + relativeEnd
		}
		raw := bytes.TrimSpace(data[offset:end])
		if len(raw) != 0 {
			var event agent.Event
			if err := json.Unmarshal(raw, &event); err != nil {
				finalLine := end == len(data) || (hasNewline && end+1 == len(data))
				if !finalLine {
					return false, fmt.Errorf("store: invalid journal line %d: %w", lineNo, err)
				}
				if err := recoverTornTail(path, data, offset); err != nil {
					return false, err
				}
				return false, nil
			}
		}
		if !hasNewline {
			break
		}
		offset = end + 1
	}
	return needsSeparator, nil
}

func recoverTornTail(path string, data []byte, validBytes int) error {
	backup, err := writeRecoveryCopy(path, data)
	if err != nil {
		return err
	}
	f, err := openFileNoFollow(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("store: open journal for tail recovery: %w", err)
	}
	if err := f.Truncate(int64(validBytes)); err != nil {
		_ = f.Close()
		return fmt.Errorf("store: truncate journal tail (backup %s): %w", backup, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("store: sync journal tail recovery (backup %s): %w", backup, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("store: close journal after tail recovery (backup %s): %w", backup, err)
	}
	return nil
}

func writeRecoveryCopy(path string, data []byte) (string, error) {
	for attempt := 0; attempt < 32; attempt++ {
		// Keep the recovery artifact outside the *.jsonl session namespace so
		// latest-session discovery and purge never mistake it for a session.
		backup := fmt.Sprintf("%s.recovery-%d-%02d", path, time.Now().UnixNano(), attempt)
		f, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", fmt.Errorf("store: create journal recovery copy: %w", err)
		}
		_, writeErr := f.Write(data)
		if writeErr == nil {
			writeErr = f.Sync()
		}
		closeErr := f.Close()
		if writeErr != nil {
			_ = os.Remove(backup)
			return "", fmt.Errorf("store: write journal recovery copy: %w", writeErr)
		}
		if closeErr != nil {
			_ = os.Remove(backup)
			return "", fmt.Errorf("store: close journal recovery copy: %w", closeErr)
		}
		return backup, nil
	}
	return "", errors.New("store: could not allocate journal recovery copy")
}

// Sync flushes buffered bytes and calls fsync without closing the journal.
// The agent uses this for mutation and permission events whose loss would
// invalidate recovery or auditability.
func (j *JSONL) Sync() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.syncLocked()
}

func (j *JSONL) syncLocked() error {
	if j.f == nil {
		return nil
	}
	if err := j.w.Flush(); err != nil {
		return err
	}
	return j.f.Sync()
}

// Close flushes and syncs.
func (j *JSONL) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return nil
	}
	err := j.syncLocked()
	if cerr := j.f.Close(); err == nil {
		err = cerr
	}
	j.f = nil
	return err
}

// MaxJournalBytes is the size trigger for journal rotation: when --continue
// opens a journal larger than this, the full history is archived aside and
// the live file is rewritten to contain only the live branch (see
// ReadLiveBranch). A single session journal has no other size bound —
// append-only by design — so without rotation a very long session would grow
// without limit on disk (and force --continue to parse it all).
const MaxJournalBytes = 32 << 20

// Read parses a whole session. It is deliberately forgiving: a blank line
// is skipped, and an unparsable final line is assumed to be a crash during
// Append and dropped. An unparsable line anywhere else is a real error.
func Read(path string) ([]agent.Event, error) {
	var out []agent.Event
	err := Scan(path, func(e agent.Event) error {
		out = append(out, e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Scan streams the journal at path in file order, invoking fn for each
// parsed event. It carries Read's forgiveness (blank lines skipped, a
// truncated final line dropped) but keeps only O(1) events in memory, so
// --export --redact and --continue can process huge journals without loading
// them wholesale.
func Scan(path string, fn func(agent.Event) error) error {
	return scanLines(path, func(_ []byte, e agent.Event, _ int) error {
		return fn(e)
	})
}

// scanLines streams the journal's raw lines, applying Read's forgiveness
// rules: blank lines are skipped and an unparsable final line is assumed to
// be a torn Append and dropped. An unparsable line anywhere else is an error.
// The raw slice is only valid for the duration of fn.
func scanLines(path string, fn func(raw []byte, e agent.Event, line int) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

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
				return fmt.Errorf("%s:%d: %w", path, line, err)
			}
			break
		}
		if err := fn(raw, e, line); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			return err
		}
	}
	return nil
}

// ReadLiveBranch returns the journal's live branch — the events --continue
// needs to seed a session — plus the total event count, without loading the
// whole file. It streams the file twice: the first pass finds the newest
// Compact event (and counts events); the second keeps only that Compact
// event and events with Seq >= its FirstKept, then resolves the branch with
// agent.Live. Memory stays O(live branch) instead of O(journal).
//
// When keepRaw is true the raw source lines of the live events are also
// returned, so rotation can rewrite the journal byte-for-byte (unknown
// fields and formatting preserved).
func ReadLiveBranch(path string, keepRaw bool) (live []agent.Event, raw [][]byte, total int, err error) {
	var newestCompact *agent.Event
	if err := Scan(path, func(e agent.Event) error {
		total++
		if e.Type == agent.Compact {
			cp := e
			newestCompact = &cp
		}
		return nil
	}); err != nil {
		return nil, nil, 0, err
	}
	if newestCompact == nil {
		// No compaction: the whole file may be live. Fall back to one full
		// read rather than a second streaming pass over everything.
		evs, err := Read(path)
		if err != nil {
			return nil, nil, 0, err
		}
		if !keepRaw {
			return evs, nil, len(evs), nil
		}
		var rawLines [][]byte
		if err := scanLines(path, func(raw []byte, _ agent.Event, _ int) error {
			cp := make([]byte, len(raw)+1)
			copy(cp, raw)
			cp[len(raw)] = '\n'
			rawLines = append(rawLines, cp)
			return nil
		}); err != nil {
			return nil, nil, 0, err
		}
		return evs, rawLines, len(evs), nil
	}
	firstKept := newestCompact.FirstKept
	if firstKept < 1 {
		firstKept = 1
	}
	var kept []agent.Event
	var keptRaw [][]byte
	if err := scanLines(path, func(raw []byte, e agent.Event, _ int) error {
		if e.Type != agent.Compact && e.Seq < firstKept {
			return nil
		}
		kept = append(kept, e)
		if keepRaw {
			cp := make([]byte, len(raw)+1)
			copy(cp, raw)
			cp[len(raw)] = '\n'
			keptRaw = append(keptRaw, cp)
		}
		return nil
	}); err != nil {
		return nil, nil, 0, err
	}
	live = agent.Live(kept)
	if !keepRaw {
		return live, nil, total, nil
	}
	// Align raw lines with the resolved live branch: the branch is the newest
	// Compact event plus kept events with Seq >= firstKept. Seq order is file
	// order, so map raw lines by Seq.
	bySeq := make(map[int][]byte, len(keptRaw))
	for i, e := range kept {
		bySeq[e.Seq] = keptRaw[i]
	}
	raw = make([][]byte, 0, len(live))
	for _, e := range live {
		line, ok := bySeq[e.Seq]
		if !ok {
			return nil, nil, 0, fmt.Errorf("%s: live event seq %d missing from kept set", path, e.Seq)
		}
		raw = append(raw, line)
	}
	return live, raw, total, nil
}
