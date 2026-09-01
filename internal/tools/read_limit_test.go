package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadFileLargeFile: what does read_file return for a file over the
// ~200-line single-read limit? Measures the actual line count delivered.
func TestReadFileLargeFile(t *testing.T) {
	r, dir := newReg(t)
	path := filepath.Join(dir, "big.go")
	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteString("line number " + string(rune('a'+i%26)) + "\n")
	}
	os.WriteFile(path, []byte(b.String()), 0o644)

	raw, _ := json.Marshal(map[string]any{"path": "big.go"})
	out, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw))
	if err != nil || !ok {
		t.Fatalf("read_file: ok=%v err=%v", ok, err)
	}
	lines := strings.Count(out, "\n")
	t.Logf("400-line file returned %d lines (%d chars)", lines, len(out))

	// Estimate tokens: ~4 chars/token for Arabic-heavy, ~1 token/word for code.
	// read_file must deliver the whole file (no hidden truncation in the tool);
	// the TPM ceiling is a provider-side limit, not a tool-side one.
	if lines < 400 {
		t.Errorf("read_file truncated a 400-line file to %d lines", lines)
	}
}
