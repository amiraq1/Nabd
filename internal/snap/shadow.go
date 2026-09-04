package snap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrShadowMissing    = errors.New("recovery content is missing")
	ErrShadowCorruption = errors.New("recovery content is damaged")
	ErrAtomicPublishUnsupported = errors.New("atomic publish unsupported")
	ErrShadowInvalidID  = errors.New("invalid shadow identifier")
)

// State is a file at one instant. Absent is a state, not an error: the
// undo of "created" is "delete", and only the recorded absence knows it.
type State struct {
	Rel    string      `json:"rel"`
	Absent bool        `json:"absent,omitempty"`
	Blob   string      `json:"blob,omitempty"` // s256:… fallback
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
	store string
}

// New sets up the shadow store for a root. We use .ag/shadow as a
// durable content-addressed store. We DO NOT rely on git gc or git
// for keeping objects reachable.
func New(root string) (*Shadow, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	s := &Shadow{root: abs, store: filepath.Join(abs, ".ag", "shadow")}
	return s, nil
}

func (s *Shadow) UsesGit() bool    { return false }
func (s *Shadow) StoreDir() string { return s.store }

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
	return s.RestoreAt(abs, st)
}

func (s *Shadow) RestoreAt(abs string, st State) error {
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
		// Legacy record: preserve existing mode if it exists
		if fi, err := os.Stat(abs); err == nil {
			mode = fi.Mode().Perm()
		} else {
			mode = 0o644 // conservative documented default for absent file with unknown mode
		}
	}
	return WriteAtomic(abs, data, mode)
}

// WriteAtomic replaces a file in one step: write a sibling, fsync it,
// rename over the target. A phone loses power mid-write; a half-written
// source file is worse than no write at all.
//
// This is where tmp+rename belongs -- not in the append-only journal,
// where a single O_APPEND write already carries a whole line.

// --- content store ---

func validateID(id string) error {
	if len(id) != 5+64 || !strings.HasPrefix(id, "s256:") {
		return fmt.Errorf("%w: invalid format: %s", ErrShadowInvalidID, id)
	}
	hexPart := id[5:]
	for i := 0; i < len(hexPart); i++ {
		c := hexPart[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return fmt.Errorf("%w: invalid character in digest: %q", ErrShadowInvalidID, c)
		}
	}
	return nil
}

func (s *Shadow) get(id string) ([]byte, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: empty blob id", ErrShadowInvalidID)
	}
	if err := validateID(id); err != nil {
		return nil, err
	}

	p := filepath.Join(s.store, id[5:7], id[7:])
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %v", ErrShadowMissing, err)
		}
		return nil, fmt.Errorf("%w: %v", ErrShadowMissing, err) // Or explicit wrapped IO
	}

	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != id[5:] {
		return nil, fmt.Errorf("%w: blob %s checksum mismatch", ErrShadowCorruption, id)
	}

	return data, nil
}
func WriteAtomic(abs string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".ag-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()

	// Ensure cleanup on failure
	defer os.Remove(name)

	// 1. write all content and verify write errors
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	// 2. apply the intended file mode
	if err := os.Chmod(name, mode.Perm()); err != nil {
		tmp.Close()
		return err
	}
	// 3. sync the temporary file
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	// 4. close it and check the close error
	if err := tmp.Close(); err != nil {
		return err
	}
	// 5. rename it into place
	if err := os.Rename(name, abs); err != nil {
		return err
	}
	// 6. sync the destination directory
	if err := syncDir(dir); err != nil {
		return err // Propagate supported directory-sync failures
	}

	return nil
}
func (s *Shadow) put(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	id := "s256:" + hex.EncodeToString(sum[:])
	p := filepath.Join(s.store, id[5:7], id[7:])

	if existingData, err := os.ReadFile(p); err == nil {
		if bytes.Equal(existingData, data) {
			return id, nil // content-addressed: already there and matches
		}
		return "", fmt.Errorf("%w: blob collision or corruption: %s exists with different content", ErrShadowCorruption, p)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read existing blob: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp(filepath.Dir(p), ".ag-*.tmp")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	defer os.Remove(name)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := os.Chmod(name, 0o400); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}

	// Use platform-specific atomic no-replace publication
	if renameErr := renameNoReplace(name, p); renameErr != nil {
		if os.IsExist(renameErr) {
			if existingData, readErr := os.ReadFile(p); readErr == nil {
				if bytes.Equal(existingData, data) {
					os.Remove(name)
					return id, nil
				}
				os.Remove(name)
				return "", fmt.Errorf("%w: blob collision or corruption", ErrShadowCorruption)
			}
			os.Remove(name)
			return "", fmt.Errorf("read existing blob: %w", renameErr)
		}
		os.Remove(name)
		return "", fmt.Errorf("failed to persist blob via renameNoReplace: %w", renameErr)
	}

	// Sync dir
	if err := syncDir(filepath.Dir(p)); err != nil {
		return "", err
	}

	return id, nil
}
