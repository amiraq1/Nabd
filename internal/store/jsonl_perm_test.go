package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nabd/internal/agent"
)

// permBits returns the permission bits of path, masking off the file-type bits.
// Tests must call this helper and compare against a literal constant; they must
// never rely on the process umask to produce the expected value.
func permBits(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("permBits: Stat(%q): %v", path, err)
	}
	return fi.Mode().Perm()
}

// TestNewJSONL_NewFileIsPrivate verifies that a brand-new journal file is
// created with mode 0o600 regardless of the process umask.
//
// Rationale: the file is opened with os.O_CREATE|0o600 and then immediately
// Fchmod'd.  We cannot rely on O_CREATE mode alone because the umask is
// applied at open time, but Fchmod is not masked.
func TestNewJSONL_NewFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	j, err := NewJSONL(path)
	if err != nil {
		t.Fatalf("NewJSONL: %v", err)
	}
	defer j.Close()

	if got := permBits(t, path); got != 0o600 {
		t.Errorf("new journal mode = 0o%o, want 0o600", got)
	}
}

// TestNewJSONL_LegacyFileIsHardened verifies that opening an existing journal
// that was created with the old 0o644 mode tightens it to 0o600 before any
// data is written.
func TestNewJSONL_LegacyFileIsHardened(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.jsonl")

	// Simulate a legacy file written by an older version of nabd.
	if err := os.WriteFile(path, []byte(`{"seq":1,"t":"2026-01-01T00:00:00Z","type":"run_start"}`+"\n"), 0o644); err != nil {
		t.Fatalf("setup legacy file: %v", err)
	}
	// Force 0o644 in case umask removed group/other bits already.
	_ = os.Chmod(path, 0o644)
	if permBits(t, path) != 0o644 {
		t.Skip("filesystem does not honour 0o644 creation mode; skipping legacy hardening test")
	}

	j, err := NewJSONL(path)
	if err != nil {
		t.Fatalf("NewJSONL on legacy file: %v", err)
	}
	defer j.Close()

	if got := permBits(t, path); got != 0o600 {
		t.Errorf("legacy journal mode after open = 0o%o, want 0o600", got)
	}
}

// TestNewJSONL_AppendAndReadAfterHarden verifies that the existing content of
// a legacy file is preserved after hardening, and that subsequent Append +
// Read works correctly.
func TestNewJSONL_AppendAndReadAfterHarden(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.jsonl")

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	first := agent.Event{Seq: 1, Time: base, Type: agent.RunStart, Text: "prior"}

	// Produce a legacy 0o644 file with one event.
	{
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(first.ForStore())
		_, _ = f.Write(b)
		_, _ = f.Write([]byte("\n"))
		_ = f.Close()
		// Ensure 0o644 even if umask stripped bits.
		_ = os.Chmod(path, 0o644)
	}

	j, err := NewJSONL(path)
	if err != nil {
		t.Fatalf("NewJSONL: %v", err)
	}

	second := agent.Event{Seq: 2, Parent: 1, Time: base.Add(time.Second), Type: agent.UserMsg, Text: "مرحبا"}
	if err := j.Append(second); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	evs, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("got %d events, want 2", len(evs))
	}
	if evs[0].Seq != 1 || evs[1].Seq != 2 {
		t.Errorf("wrong events: seq[0]=%d seq[1]=%d", evs[0].Seq, evs[1].Seq)
	}
}

// TestNewJSONL_MissingParentDirIsPrivate verifies that a directory nabd
// itself creates for a journal is created with mode 0o700, and that the
// result does not depend on the process umask.
//
// A permissive umask must not turn the created directory into 0o755: the
// journal file is 0o600, so the file contents stay protected either way, but a
// world-listable directory exposes how many sessions exist and what they are
// named. This test is meaningful on CI (GitHub's ubuntu runners use umask
// 0022) and on any machine with a permissive umask; on a developer shell with
// umask 0077 the deviation is masked (0o755 &^ 0o077 == 0o700), so the test
// passes there for the wrong reason. Do not "fix" it by relaxing the
// assertion to whatever the local umask yields — that would license the bug.
func TestNewJSONL_MissingParentDirIsPrivate(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "created-by-nabd")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("precondition: %q must not exist (Stat err = %v)", dir, err)
	}

	path := filepath.Join(dir, "session.jsonl")
	j, err := NewJSONL(path)
	if err != nil {
		t.Fatalf("NewJSONL: %v", err)
	}
	defer j.Close()

	if got := permBits(t, dir); got != 0o700 {
		t.Errorf("directory created by NewJSONL = 0o%o, want 0o700", got)
	}
	if got := permBits(t, path); got != 0o600 {
		t.Errorf("journal mode = 0o%o, want 0o600", got)
	}
}

// TestNewJSONL_CreatedAncestorsArePrivate verifies the multi-level half of the
// contract: when --dir names a path whose ancestors do not exist either, every
// directory nabd creates along the way is private. The leaf is pinned to
// exactly 0o700 by the explicit Chmod; ancestors get MkdirAll's 0o700 before
// the umask, so they can be narrower but never wider (no group/other bits).
func TestNewJSONL_CreatedAncestorsArePrivate(t *testing.T) {
	base := t.TempDir()
	leaf := filepath.Join(base, "made-by-nabd", "nested", "sessions")
	if _, err := os.Stat(filepath.Join(base, "made-by-nabd")); !os.IsNotExist(err) {
		t.Fatalf("precondition: ancestor must not exist (Stat err = %v)", err)
	}

	path := filepath.Join(leaf, "session.jsonl")
	j, err := NewJSONL(path)
	if err != nil {
		t.Fatalf("NewJSONL: %v", err)
	}
	defer j.Close()

	if got := permBits(t, path); got != 0o600 {
		t.Errorf("journal mode = 0o%o, want 0o600", got)
	}
	if got := permBits(t, leaf); got != 0o700 {
		t.Errorf("leaf directory mode = 0o%o, want exactly 0o700", got)
	}
	for _, ancestor := range []string{
		filepath.Join(base, "made-by-nabd"),
		filepath.Join(base, "made-by-nabd", "nested"),
	} {
		got := permBits(t, ancestor)
		if got&0o077 != 0 {
			t.Errorf("ancestor %s mode = 0o%o, want no group/other bits", ancestor, got)
		}
	}
}

// TestNewJSONL_ExistingParentDirIsNotTouched verifies the other half of the
// --dir contract: a directory that already exists keeps whatever mode the
// caller gave it, even a wide one. nabd never widens and never tightens a
// directory it did not create.
func TestNewJSONL_ExistingParentDirIsNotTouched(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if permBits(t, dir) != 0o755 {
		t.Skip("filesystem does not honour 0o755 mode; skipping existing-dir test")
	}

	path := filepath.Join(dir, "session.jsonl")
	j, err := NewJSONL(path)
	if err != nil {
		t.Fatalf("NewJSONL: %v", err)
	}
	defer j.Close()

	if got := permBits(t, path); got != 0o600 {
		t.Errorf("journal mode = 0o%o, want 0o600", got)
	}
	if got := permBits(t, dir); got != 0o755 {
		t.Errorf("existing --dir mode changed to 0o%o, want 0o755 (untouched)", got)
	}
}

// TestNewJSONL_CustomDirPermUnchanged verifies that when the caller passes a
// custom directory path, NewJSONL does NOT widen or narrow the directory
// permissions.  The file itself must still be private.
func TestNewJSONL_CustomDirPermUnchanged(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Skipf("cannot chmod temp dir: %v", err)
	}

	path := filepath.Join(dir, "session.jsonl")
	j, err := NewJSONL(path)
	if err != nil {
		t.Fatalf("NewJSONL: %v", err)
	}
	j.Close()

	// The file must be private.
	if got := permBits(t, path); got != 0o600 {
		t.Errorf("journal mode = 0o%o, want 0o600", got)
	}
	// The caller-supplied directory must be unchanged.
	if got := permBits(t, dir); got != 0o755 {
		t.Errorf("custom dir mode changed to 0o%o, want 0o755 (unchanged)", got)
	}
}

// TestNewJSONL_ExistingPrivateFileUnchanged verifies that opening an already-
// private (0o600) file does not produce an error.
func TestNewJSONL_ExistingPrivateFileUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	j, err := NewJSONL(path)
	if err != nil {
		t.Fatalf("NewJSONL on already-private file: %v", err)
	}
	j.Close()

	if got := permBits(t, path); got != 0o600 {
		t.Errorf("mode = 0o%o, want 0o600", got)
	}
}

// TestNewJSONL_TruncatedFinalLineStillTolerated confirms that crash-tolerance
// is unaffected by the permission change.
func TestNewJSONL_TruncatedFinalLineStillTolerated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "torn.jsonl")
	body := `{"seq":1,"t":"2026-09-01T10:00:00Z","type":"run_start"}` + "\n" +
		`{"seq":2,"t":"2026-09-01T10:00:01Z","type":"run_e` // truncated

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	evs, err := Read(path)
	if err != nil {
		t.Fatalf("truncated final line must be tolerated: %v", err)
	}
	if len(evs) != 1 {
		t.Errorf("got %d events, want 1", len(evs))
	}
}
