// Package snap records what a file looked like before the agent touched
// it, and puts it back. There is no separate store to maintain: when the
// project is a git repository, git's own object database holds the
// content, and the "shadow" is two hashes in the journal.
package snap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// State is a file at one instant. Absent is a state, not an error: the
// undo of "created" is "delete", and only the recorded absence knows it.
type State struct {
	Rel    string      `json:"rel"`
	Absent bool        `json:"absent,omitempty"`
	Blob   string      `json:"blob,omitempty"` // git oid, or s256:… fallback
	Size   int64       `json:"size,omitempty"`
	Mode   os.FileMode `json:"mode,omitempty"`
	At     time.Time   `json:"at"`
}

// Change is one reversible edit: what was there, and what is there now.
type Change struct {
	Before State `json:"before"`
	After  State `json:"after"`
}

// Shadow hashes and stores content for a single project root.
type Shadow struct {
	root  string
	git   bool
	store string // used only when git is absent
}

// New probes for git once. A git repo costs nothing: hash-object writes
// a loose object that gc leaves alone while it is reachable from nothing
// but our hash -- which is why we keep the hash in the journal.
func New(root string) (*Shadow, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	s := &Shadow{root: abs, store: filepath.Join(abs, ".ag", "shadow")}

	if _, err := exec.LookPath("git"); err == nil {
		cmd := exec.Command("git", "-C", abs, "rev-parse", "--git-dir")
		cmd.Stderr = io.Discard
		if cmd.Run() == nil {
			s.git = true
		}
	}
	return s, nil
}

func (s *Shadow) UsesGit() bool { return s.git }

// Capture reads the current state of an absolute path inside the root and
// stores its content so it can be restored later.
func (s *Shadow) Capture(abs string) (State, error) {
	rel, err := filepath.Rel(s.root, abs)
	if err != nil {
		return State{}, err
	}
	st := State{Rel: filepath.ToSlash(rel), At: time.Now().UTC()}

	fi, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		st.Absent = true
		return st, nil
	}
	if err != nil {
		return State{}, err
	}
	if fi.IsDir() {
		return State{}, fmt.Errorf("%s مجلد", st.Rel)
	}
	// A symlink is never followed here: containment already refused any
	// link that escapes, and following one would shadow the wrong file.
	if fi.Mode()&os.ModeSymlink != 0 {
		return State{}, fmt.Errorf("%s رابط رمزي", st.Rel)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return State{}, err
	}
	st.Size = int64(len(data))
	st.Mode = fi.Mode().Perm()
	if st.Blob, err = s.put(data); err != nil {
		return State{}, err
	}
	return st, nil
}

// CaptureBytes records a state for content the caller already holds. Used
// for the "after" side, so a write is not read back off disk twice.
func (s *Shadow) CaptureBytes(abs string, data []byte, mode os.FileMode) (State, error) {
	rel, err := filepath.Rel(s.root, abs)
	if err != nil {
		return State{}, err
	}
	st := State{
		Rel: filepath.ToSlash(rel), Size: int64(len(data)),
		Mode: mode.Perm(), At: time.Now().UTC(),
	}
	if st.Blob, err = s.put(data); err != nil {
		return State{}, err
	}
	return st, nil
}

// Unchanged reports whether two states describe the same content. This is
// the whole of "verification": one comparison, not a framework.
func Unchanged(a, b State) bool {
	if a.Absent || b.Absent {
		return a.Absent == b.Absent
	}
	return a.Blob != "" && a.Blob == b.Blob
}

// Read fetches the content behind a recorded state. It is the hash-to-bytes
// door: the journal keeps hashes, and a later process (e.g. a restarted
// /undo) pulls the content back through here.
func (s *Shadow) Read(id string) ([]byte, error) {
	return s.get(id)
}

// Restore puts a file back into a recorded state, atomically.
func (s *Shadow) Restore(st State) error {
	abs := filepath.Join(s.root, filepath.FromSlash(st.Rel))

	if st.Absent {
		err := os.Remove(abs)
		if errors.Is(err, os.ErrNotExist) {
			return nil // already the recorded state
		}
		return err
	}
	data, err := s.get(st.Blob)
	if err != nil {
		return err
	}
	mode := st.Mode
	if mode == 0 {
		mode = 0o644
	}
	return WriteAtomic(abs, data, mode)
}

// WriteAtomic replaces a file in one step: write a sibling, fsync it,
// rename over the target. A phone loses power mid-write; a half-written
// source file is worse than no write at all.
//
// This is where tmp+rename belongs -- not in the append-only journal,
// where a single O_APPEND write already carries a whole line.
func WriteAtomic(abs string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".ag-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
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
	if err := os.Chmod(name, mode.Perm()); err != nil {
		return err
	}
	return os.Rename(name, abs)
}

// --- content store ---

// put stores content and returns its identifier.
func (s *Shadow) put(data []byte) (string, error) {
	if s.git {
		cmd := exec.Command("git", "-C", s.root, "hash-object", "-w", "--stdin")
		cmd.Stdin = bytes.NewReader(data)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("git hash-object: %s", strings.TrimSpace(errb.String()))
		}
		return strings.TrimSpace(out.String()), nil
	}

	sum := sha256.Sum256(data)
	id := "s256:" + hex.EncodeToString(sum[:])
	p := filepath.Join(s.store, id[5:7], id[7:])
	if _, err := os.Stat(p); err == nil {
		return id, nil // content-addressed: already there
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if err := WriteAtomic(p, data, 0o600); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Shadow) get(id string) ([]byte, error) {
	if id == "" {
		return nil, errors.New("لا محتوى محفوظ")
	}
	if strings.HasPrefix(id, "s256:") {
		return os.ReadFile(filepath.Join(s.store, id[5:7], id[7:]))
	}
	cmd := exec.Command("git", "-C", s.root, "cat-file", "blob", id)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git cat-file %s: %s", id[:min(8, len(id))],
			strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
