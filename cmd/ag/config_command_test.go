package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigShowRedactsCredentials(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(p, []byte("GROQ_API_KEY=secret\nNABD_PROVIDER=groq\n"), 0o600); err != nil { t.Fatal(err) }
	t.Setenv("NABD_CONFIG", p)
	var out, errOut bytes.Buffer
	if code := runConfigCommand([]string{"show", "--redacted"}, &out, &errOut); code != 0 { t.Fatalf("code=%d stderr=%s", code, errOut.String()) }
	if strings.Contains(out.String(), "secret") || !strings.Contains(out.String(), "GROQ_API_KEY=<redacted>") { t.Fatalf("unsafe output: %q", out.String()) }
	if !strings.Contains(out.String(), "NABD_PROVIDER=groq") { t.Fatalf("missing setting: %q", out.String()) }
}

func TestConfigShowRequiresRedacted(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := runConfigCommand([]string{"show"}, &out, &errOut); code != 2 { t.Fatalf("code=%d", code) }
}
