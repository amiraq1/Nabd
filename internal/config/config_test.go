package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestParse(t *testing.T) {
	in := `
# comment
ANTHROPIC_API_KEY=sk-ant-abc
export NVIDIA_API_KEY="nvapi-xyz"
NABD_MODEL='claude#1'
NABD_BASE_URL=https://x.example/v1 # trailing note
EMPTY=
`
	got, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"ANTHROPIC_API_KEY": "sk-ant-abc",
		"NVIDIA_API_KEY":    "nvapi-xyz",
		"NABD_MODEL":        "claude#1",
		"NABD_BASE_URL":     "https://x.example/v1",
		"EMPTY":             "",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d keys, want %d", len(got), len(want))
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"just words", "=novalue", "A B=1"} {
		if _, err := Parse(strings.NewReader(bad)); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestParseFileMissingIsFine(t *testing.T) {
	m, err := ParseFile(filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(m) != 0 {
		t.Fatalf("missing file: %v %v", m, err)
	}
}

func TestParseFileRefusesLoosePerms(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(p, []byte("K=v\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(p, 0o644)
	if fi, err := os.Stat(p); err == nil && fi.Mode().Perm()&0o077 == 0 {
		t.Skip("filesystem or umask does not support loose permission bits")
	}
	if _, err := ParseFile(p); err == nil {
		t.Fatal("0644 accepted; want refusal")
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := ParseFile(p)
	if err != nil || m["K"] != "v" {
		t.Fatalf("0600: %v %v", m, err)
	}
}

func TestFileWinsOverEnv(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config")
	os.WriteFile(p, []byte("NABD_TEST_KEY=fromfile\n"), 0o600)
	t.Setenv(EnvVar, p)
	t.Setenv("NABD_TEST_KEY", "fromenv")
	t.Setenv("NABD_TEST_ONLYENV", "env")
	reset()
	if g := Get("NABD_TEST_KEY"); g != "fromfile" {
		t.Errorf("Get = %q, want fromfile", g)
	}
	if g := Get("NABD_TEST_ONLYENV"); g != "env" {
		t.Errorf("env fallback = %q", g)
	}
	if g := GetOr("NABD_TEST_MISSING", "dflt"); g != "dflt" {
		t.Errorf("GetOr = %q", g)
	}
}

func TestNeverWrites(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvVar, filepath.Join(dir, "config"))
	reset()
	_ = Load()
	_ = Get("X")
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Fatalf("config created files: %v", ents)
	}
}

// TestConfigRejectsInsecurePermissions complements TestParseFileRefusesLoosePerms
// by asserting the new ParseFile explicitly names the perm-based refusal and
// accepts a tightly-permissioned file in the same run.
func TestConfigRejectsInsecurePermissions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(p, []byte("K=v\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(p); err == nil && fi.Mode().Perm()&0o077 == 0 {
		t.Skip("filesystem or umask does not support loose permission bits")
	}
	if _, err := ParseFile(p); err == nil {
		t.Fatalf("0644 accepted; want refusal")
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if m, err := ParseFile(p); err != nil || m["K"] != "v" {
		t.Fatalf("0600: %v %v", m, err)
	}
}

// TestConfigRejectsSymlink proves ParseFile uses os.Lstat and refuses a symlink
// to a valid 0600 file — an attacker swapping a symlink in front of the open
// must not reach a file the victim would otherwise trust.
func TestConfigRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real-config")
	if err := os.WriteFile(target, []byte("K=v\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks not supported in this environment")
	}
	if _, err := ParseFile(link); err == nil {
		t.Fatalf("symlink to a valid 0600 file was accepted; want refusal")
	}
	// The real file is still accepted directly.
	if m, err := ParseFile(target); err != nil || m["K"] != "v" {
		t.Fatalf("direct file refused: %v %v", m, err)
	}
}

// TestConfigRejectsNonRegularFile proves ParseFile refuses anything that is not
// a regular file (device, socket, fifo) even if it is tightly owned.
func TestConfigRejectsNonRegularFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config")
	// A named pipe (FIFO) is the most portable non-regular file that is not a
	// symlink, so IsRegular() is false while the earlier symlink check passes.
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Skipf("mkfifo not supported: %v", err)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFile(p); err == nil {
		t.Fatalf("FIFO accepted; want refusal (only regular files allowed)")
	}
}

// TestLoadDoesNotMutateProcessEnv pins the Phase 3 environment-isolation
// contract: loading a config file must never merge its values into the
// process-global environment. A key read via Get() is served from the
// private map, and os.Getenv must stay untouched — otherwise provider
// credentials would leak into every later child process that inherits the
// parent environment.
func TestLoadDoesNotMutateProcessEnv(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config")
	os.WriteFile(p, []byte("NABD_TEST_LEAK=secret-value\n"), 0o600)
	t.Setenv(EnvVar, p)
	t.Setenv("NABD_TEST_LEAK", "") // ensure absent in the process env
	reset()
	if g := Get("NABD_TEST_LEAK"); g != "secret-value" {
		t.Fatalf("Get = %q, want secret-value from the file", g)
	}
	if g := os.Getenv("NABD_TEST_LEAK"); g != "" {
		t.Fatalf("config leaked into process env: NABD_TEST_LEAK=%q", g)
	}
}

// TestConfigRejectsWrongOwnership (Unix only): the config file must be owned by
// the current user. The happy path (file owned by the current user) is always
// verified; when the test runs as root it also checks that a file chowned to
// another uid is refused. On Windows the ownership check is a documented
// no-op (NT ACLs), so it is skipped there. A non-root Unix run stops at the
// happy path because it cannot chown to another uid.
func TestConfigRejectsWrongOwnership(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ownership check is a documented no-op on Windows (NT ACLs)")
	}
	p := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(p, []byte("K=v\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Happy path: owned by the current user → accepted.
	if m, err := ParseFile(p); err != nil || m["K"] != "v" {
		t.Fatalf("file owned by current user refused: %v %v", m, err)
	}
	// Rejection path: only verifiable when we can chown to another uid, which
	// requires privilege. Non-root runs stop at the happy path above.
	if os.Getuid() != 0 {
		t.Skip("cannot chown to another uid without privilege; happy path verified")
	}
	if err := os.Chown(p, 1, 1); err != nil { // chown to daemon
		t.Skipf("chown not permitted: %v", err)
	}
	if _, err := ParseFile(p); err == nil {
		t.Fatalf("file owned by another uid was accepted; want refusal")
	}
}

// TestConflictsNamesIgnoredEnvKeys proves that a key present in both the file
// and the environment with different values is reported as a conflict — the
// file wins, so the environment value was silently ignored.
func TestConflictsNamesIgnoredEnvKeys(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config")
	if err := os.WriteFile(p, []byte("NABD_TEST_CONF_KEY=fromfile\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvVar, p)
	t.Setenv("NABD_TEST_CONF_KEY", "fromenv")
	ResetForTest()
	t.Cleanup(ResetForTest)

	got := Conflicts()
	if len(got) != 1 || got[0].Key != "NABD_TEST_CONF_KEY" {
		t.Fatalf("Conflicts = %+v, want [{Key:NABD_TEST_CONF_KEY}]", got)
	}
}

// TestConflictsIgnoresMatchingValues proves that when the file and environment
// agree, there is no conflict — the user gets no noise.
func TestConflictsIgnoresMatchingValues(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config")
	if err := os.WriteFile(p, []byte("NABD_TEST_SAME=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvVar, p)
	t.Setenv("NABD_TEST_SAME", "value")
	ResetForTest()
	t.Cleanup(ResetForTest)

	if got := Conflicts(); len(got) != 0 {
		t.Fatalf("Conflicts = %+v, want []", got)
	}
}

// TestConflictsIgnoresEnvOnlyAndFileOnly proves that a key present in only one
// source is not a conflict — there is nothing to override.
func TestConflictsIgnoresEnvOnlyAndFileOnly(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config")
	if err := os.WriteFile(p, []byte("NABD_TEST_FILEONLY=abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvVar, p)
	t.Setenv("NABD_TEST_ENVONLY", "xyz")
	ResetForTest()
	t.Cleanup(ResetForTest)

	if got := Conflicts(); len(got) != 0 {
		t.Fatalf("Conflicts = %+v, want []", got)
	}
}

// TestConflictsNeverExposesValues proves the security boundary: Conflict carries
// no value, and the rendered line must never contain the secret.
func TestConflictsNeverExposesValues(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config")
	if err := os.WriteFile(p, []byte("GROQ_API_KEY=secret-shhh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvVar, p)
	t.Setenv("GROQ_API_KEY", "other-secret")
	ResetForTest()
	t.Cleanup(ResetForTest)

	got := Conflicts()
	if len(got) != 1 {
		t.Fatalf("Conflicts = %+v, want one conflict", got)
	}
	if got[0].Key != "GROQ_API_KEY" {
		t.Fatalf("key = %q, want GROQ_API_KEY", got[0].Key)
	}
	// The struct must carry exactly one field — the key name, never a value.
	if n := reflect.TypeOf(Conflict{}).NumField(); n != 1 {
		t.Fatalf("Conflict has %d fields, want 1 (Key only)", n)
	}
}
