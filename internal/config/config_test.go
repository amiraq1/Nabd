package config

import (
	"os"
	"path/filepath"
	"strings"
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
