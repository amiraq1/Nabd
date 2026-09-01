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
// byte cap? The tool must truncate at a line boundary and mark it.
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
	t.Logf("TAIL: %q", strings.TrimSpace(out[strings.LastIndex(out, "\n"):]))
}

// TestReadFileSmallFileUntouched: a small file must come back whole, with
// no truncation tail and no truncated flag.
func TestReadFileSmallFileUntouched(t *testing.T) {
	r, dir := newReg(t)
	path := filepath.Join(dir, "small.md")
	os.WriteFile(path, []byte("سطر واحد\nسطر اثنان\nسطر ثلاثة\n"), 0o644)

	raw, _ := json.Marshal(map[string]any{"path": "small.md"})
	out, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw))
	if err != nil || !ok {
		t.Fatalf("read_file: ok=%v err=%v", ok, err)
	}
	if strings.Contains(out, "TRUNCATED") {
		t.Errorf("small file must not be truncated, got: %q", out)
	}
	if r.ConsumeTruncated() {
		t.Error("truncation flag set for a small file")
	}
	if !strings.Contains(out, "1|سطر واحد") || !strings.Contains(out, "3|سطر ثلاثة") {
		t.Errorf("small file content lost: %q", out)
	}
}

// TestReadFileByteCapTruncates: a file larger than maxBytes must be cut at
// a line boundary, carry the explicit tail, and set the truncated flag.
func TestReadFileByteCapTruncates(t *testing.T) {
	r, dir := newReg(t)
	path := filepath.Join(dir, "wide.go")
	// 300 lines × 120 chars ≈ 36 KB — far over the 16 KiB cap.
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString(strings.Repeat("x", 120) + "\n")
	}
	os.WriteFile(path, []byte(b.String()), 0o644)

	raw, _ := json.Marshal(map[string]any{"path": "wide.go"})
	out, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw))
	if err != nil || !ok {
		t.Fatalf("read_file: ok=%v err=%v", ok, err)
	}
	if !r.ConsumeTruncated() {
		t.Fatal("truncation flag not set for an oversized file")
	}
	if !strings.Contains(out, "[TRUNCATED:") {
		t.Fatalf("missing explicit truncation tail in tool_result: %q", out)
	}
	if !strings.Contains(out, "use offset=") {
		t.Errorf("tail must say how to continue (offset): %q", out)
	}
	// The cut must be at a line boundary: every emitted line is whole.
	for _, ln := range strings.Split(out, "\n") {
		if ln == "" || strings.HasPrefix(ln, "[TRUNCATED") {
			continue
		}
		if !strings.HasSuffix(ln, "x") && !strings.Contains(ln, "|") {
			t.Errorf("mid-line cut detected: %q", ln)
		}
	}
	// The tail carries the stopping line and total line context.
	if !strings.Contains(out, "of 300") {
		t.Errorf("tail must say total line context (of 300): %q", out)
	}
}

// TestReadFileOffsetContinues: after a truncation, offset= continues the
// read — the continuation path must work.
func TestReadFileOffsetContinues(t *testing.T) {
	r, dir := newReg(t)
	path := filepath.Join(dir, "wide.go")
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString(strings.Repeat("y", 120) + "\n")
	}
	os.WriteFile(path, []byte(b.String()), 0o644)

	raw, _ := json.Marshal(map[string]any{"path": "wide.go", "offset": 200})
	out, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw))
	if err != nil || !ok {
		t.Fatalf("read_file offset: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(out, "200|") {
		t.Errorf("offset read must start at line 200: %q", strings.SplitN(out, "\n", 2)[0])
	}
}
