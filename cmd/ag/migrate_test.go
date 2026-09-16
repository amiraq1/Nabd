package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateCommandCLI(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config")
	if err := os.WriteFile(configPath, []byte("ANTHROPIC_API_KEY=sk-ant-testcli12345678\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := runMigrateCommand([]string{"--dir", tmp}, &out, &errOut)
	if code != 0 {
		t.Fatalf("runMigrateCommand failed with code %d: %s", code, errOut.String())
	}

	outStr := out.String()
	if !strings.Contains(outStr, "Migration summary:") {
		t.Errorf("expected migration summary in stdout, got: %q", outStr)
	}
	if strings.Contains(outStr, "sk-ant-testcli12345678") {
		t.Fatalf("secret leaked in CLI output: %q", outStr)
	}

	bakPath := filepath.Join(tmp, "config.bak")
	if _, err := os.Stat(bakPath); err != nil {
		t.Errorf("config.bak not found: %v", err)
	}
}
