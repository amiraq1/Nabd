package main

import (
	"os"
	"path/filepath"
	"testing"
)

// dirPermBits returns the permission bits of a directory path.
// Tests must compare against a literal constant, never assume a umask.
func dirPermBits(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("dirPermBits: Stat(%q): %v", path, err)
	}
	return fi.Mode().Perm()
}

// TestEnsureDefaultSessionDir_NewDir verifies that a freshly created default
// session directory has mode 0o700.
func TestEnsureDefaultSessionDir_NewDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "sessions")

	if err := ensureDefaultSessionDir(dir); err != nil {
		t.Fatalf("ensureDefaultSessionDir: %v", err)
	}
	if got := dirPermBits(t, dir); got != 0o700 {
		t.Errorf("new default sessions dir mode = 0o%o, want 0o700", got)
	}
}

// TestEnsureDefaultSessionDir_LegacyDirIsHardened verifies that an existing
// directory with mode 0o755 (the legacy default) is tightened to 0o700.
func TestEnsureDefaultSessionDir_LegacyDirIsHardened(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Skipf("cannot chmod temp dir: %v", err)
	}
	if dirPermBits(t, dir) != 0o755 {
		t.Skip("filesystem does not honour 0o755 mode; skipping legacy dir hardening test")
	}

	if err := ensureDefaultSessionDir(dir); err != nil {
		t.Fatalf("ensureDefaultSessionDir: %v", err)
	}
	if got := dirPermBits(t, dir); got != 0o700 {
		t.Errorf("legacy sessions dir mode after harden = 0o%o, want 0o700", got)
	}
}

// TestEnsureDefaultSessionDir_AlreadyPrivateIsIdempotent verifies that calling
// ensureDefaultSessionDir on an already-0o700 directory is a no-op (no error).
func TestEnsureDefaultSessionDir_AlreadyPrivateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Skipf("cannot chmod temp dir: %v", err)
	}

	if err := ensureDefaultSessionDir(dir); err != nil {
		t.Fatalf("ensureDefaultSessionDir on already-0o700 dir: %v", err)
	}
	if got := dirPermBits(t, dir); got != 0o700 {
		t.Errorf("mode changed to 0o%o, want 0o700", got)
	}
}

// TestSessionPath_CustomDirPermUnchanged verifies that sessionPath does NOT
// call ensureDefaultSessionDir (and therefore does NOT chmod) when the caller
// supplies a non-empty dir via --dir.
//
// We set the custom dir to 0o755 and assert it remains 0o755 after sessionPath.
func TestSessionPath_CustomDirPermUnchanged(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Skipf("cannot chmod temp dir: %v", err)
	}

	_, err := sessionPath(dir)
	if err != nil {
		t.Fatalf("sessionPath(custom dir): %v", err)
	}
	if got := dirPermBits(t, dir); got != 0o755 {
		t.Errorf("custom dir mode changed to 0o%o, want 0o755 (unchanged)", got)
	}
}

// TestSessionPath_DefaultDirIsPrivate verifies the end-to-end behaviour of
// sessionPath when no dir is supplied: it must create (or tighten) the default
// directory to 0o700 and return a path inside it.
//
// We intercept os.UserHomeDir by temporarily overriding HOME so that the test
// does not touch the real home directory.
func TestSessionPath_DefaultDirIsPrivate(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	p, err := sessionPath("")
	if err != nil {
		t.Fatalf("sessionPath empty dir: %v", err)
	}

	// The returned path must be inside the default sessions dir.
	wantDir := filepath.Join(fakeHome, ".ag", "sessions")
	if filepath.Dir(p) != wantDir {
		t.Errorf("journal path dir = %q, want %q", filepath.Dir(p), wantDir)
	}

	if got := dirPermBits(t, wantDir); got != 0o700 {
		t.Errorf("default sessions dir mode = 0o%o, want 0o700", got)
	}
}

// TestLatestSession_DefaultDirIsPrivate verifies that --continue (latestSession)
// hardens the default sessions directory to 0o700, matching sessionPath.
// A legacy world-readable dir must not stay readable just because the user
// resumed a session instead of starting a fresh one.
func TestLatestSession_DefaultDirIsPrivate(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	dir := filepath.Join(fakeHome, ".ag", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Chmod explicitly: MkdirAll's mode is masked by umask, so a legacy 0o755
	// directory cannot be simulated by the create mode alone.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if dirPermBits(t, dir) != 0o755 {
		t.Skip("filesystem does not honour 0o755 mode; skipping legacy dir hardening test")
	}

	// A resume-worthy session for this project root.
	sessionFile := filepath.Join(dir, "20260901-120000.000.jsonl")
	if err := os.WriteFile(sessionFile, []byte("{\"type\":\"run_start\",\"project_root\":\"/proj\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := latestSession("", "/proj")
	if err != nil {
		t.Fatalf("latestSession: %v", err)
	}
	if got != sessionFile {
		t.Errorf("got %q, want %q", got, sessionFile)
	}
	if p := dirPermBits(t, dir); p != 0o700 {
		t.Errorf("default sessions dir after --continue = 0o%o, want 0o700", p)
	}
}

// TestSessionPathResolvesDefaultDirThroughSingleSource proves sessionPath
// builds the default directory in exactly one place: the seam that
// defaultSessionDir owns. If sessionPath resolved the home directory itself,
// replacing the seam would not move the path.
func TestSessionPathResolvesDefaultDirThroughSingleSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", filepath.Join(home, "decoy"))
	orig := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = orig })

	want := filepath.Join(home, ".ag", "sessions")
	got, err := sessionPath("")
	if err != nil {
		t.Fatalf("sessionPath: %v", err)
	}
	if filepath.Dir(got) != want {
		t.Fatalf("sessionPath resolved %q, want the single default-dir source %q", filepath.Dir(got), want)
	}
	if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
		t.Fatalf("default session dir not created by the single source: %v", err)
	}
}

// TestLatestSessionResolvesDefaultDirThroughSingleSource proves --continue
// reads that same single source: with the seam pointing at a home that holds
// the session and $HOME pointing at a decoy, latestSession finds the session
// only if it goes through defaultSessionDir.
func TestLatestSessionResolvesDefaultDirThroughSingleSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", filepath.Join(home, "decoy"))
	orig := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = orig })

	wantDir := filepath.Join(home, ".ag", "sessions")
	if err := os.MkdirAll(wantDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(wantDir, "20260910-120000.000.jsonl")
	if err := os.WriteFile(sessionFile, []byte("{\"type\":\"run_start\",\"project_root\":\"/proj\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := latestSession("", "/proj")
	if err != nil {
		t.Fatalf("latestSession must read the single default-dir source: %v", err)
	}
	if got != sessionFile {
		t.Fatalf("latestSession returned %q, want %q", got, sessionFile)
	}
}

// TestLatestSession_CustomDirUnchanged proves --dir semantics are untouched:
// latestSession neither chmods nor relocates a caller-supplied directory.
func TestLatestSession_CustomDirUnchanged(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Skipf("cannot chmod temp dir: %v", err)
	}
	if dirPermBits(t, dir) != 0o755 {
		t.Skip("filesystem does not honour 0o755 mode; skipping custom dir test")
	}

	if _, err := latestSession(dir, "/proj"); err == nil {
		t.Fatal("expected a no-sessions error for an empty custom dir")
	}
	if got := dirPermBits(t, dir); got != 0o755 {
		t.Errorf("custom dir mode changed to 0o%o, want 0o755 (unchanged)", got)
	}
}
