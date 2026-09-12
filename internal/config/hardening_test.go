package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRejectsDuplicateKeys(t *testing.T) {
	if _, err := Parse(strings.NewReader("NABD_MODEL=a\nNABD_MODEL=b\n")); err == nil {
		t.Fatal("duplicate key accepted")
	}
}

func TestParseEnforcesBounds(t *testing.T) {
	if _, err := Parse(strings.NewReader("K=" + strings.Repeat("x", MaxValueBytes+1))); err == nil {
		t.Fatal("oversized value accepted")
	}
	var b strings.Builder
	for i := 0; i <= MaxKeys; i++ {
		b.WriteString("K")
		b.WriteString(strings.Repeat("X", i/26))
		b.WriteByte(byte('A' + i%26))
		b.WriteString("=v\n")
	}
	if _, err := Parse(strings.NewReader(b.String())); err == nil {
		t.Fatal("too many keys accepted")
	}
}

func TestWarningsNameUnknownKeysWithoutValues(t *testing.T) {
	const secret = "do-not-print-this-value"
	got := Warnings(map[string]string{"NABD_MODEL": "ok", "TYPO_KEY": secret})
	if len(got) != 1 || !strings.Contains(got[0], "TYPO_KEY") || strings.Contains(got[0], secret) {
		t.Fatalf("unsafe warnings: %q", got)
	}
}

func TestGetFailsClosedAfterInvalidConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(p, []byte("BROKEN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvVar, p)
	t.Setenv("GROQ_API_KEY", "environment-secret")
	ResetForTest()
	t.Cleanup(ResetForTest)
	if err := Load(); err == nil {
		t.Fatal("invalid config loaded")
	}
	if got := Get("GROQ_API_KEY"); got != "" {
		t.Fatal("invalid config fell back to environment")
	}
}

func TestPathOverrideMustBeAbsolute(t *testing.T) {
	t.Setenv(EnvVar, "project/config")
	if _, err := Path(); err == nil {
		t.Fatal("relative project-scoped override accepted")
	}
}
