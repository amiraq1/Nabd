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
	if trunc, _ := r.ConsumeTruncated(); trunc {
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
	trunc, next := r.ConsumeTruncated()
	if !trunc {
		t.Fatal("truncation flag not set for an oversized file")
	}
	if next <= 0 {
		t.Errorf("next_offset not recorded: %d", next)
	}
	if !strings.Contains(out, "[TRUNCATED:") {
		t.Fatalf("missing explicit truncation tail in tool_result: %q", out)
	}
	if !strings.Contains(out, "استخدم offset=") {
		t.Errorf("tail must say how to continue (offset): %q", out)
	}
	if !strings.Contains(out, "next_offset=") {
		t.Errorf("tail must carry explicit next_offset=: %q", out)
	}
	if !strings.Contains(out, "lines_read=") || !strings.Contains(out, "total_lines=") {
		t.Errorf("tail must carry lines_read and total_lines: %q", out)
	}
	// The cut must be at a line boundary: every emitted line is whole.
	for _, ln := range strings.Split(out, "\n") {
		if ln == "" || strings.HasPrefix(ln, "[TRUNCATED") || strings.Contains(ln, "lines_read=") {
			continue
		}
		if !strings.HasSuffix(ln, "x") && !strings.Contains(ln, "|") {
			t.Errorf("mid-line cut detected: %q", ln)
		}
	}
	// The tail carries the stopping line and total line context.
	if !strings.Contains(out, "من 300") {
		t.Errorf("tail must say total line context (من 300): %q", out)
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

// TestRunDetailedConsumesTruncationOnce: the rich read path must consume
// the registry flag exactly once; a later read cannot lose or duplicate it.
func TestRunDetailedConsumesTruncationOnce(t *testing.T) {
	r, dir := newReg(t)
	path := filepath.Join(dir, "detailed.go")
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString(strings.Repeat("d", 120) + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, _ := json.Marshal(map[string]any{"path": "detailed.go"})
	out, err := r.RunDetailed(context.Background(), "read_file", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK || !out.Truncated {
		t.Fatalf("RunDetailed outcome=%+v, want successful truncation", out)
	}
	if out.NextOffset <= 1 {
		t.Fatalf("RunDetailed next_offset=%d, want progress", out.NextOffset)
	}
	if !strings.Contains(out.Text, "[TRUNCATED:") {
		t.Fatalf("RunDetailed text lacks truncation tail: %q", out.Text)
	}
	if trunc, next := r.ConsumeTruncated(); trunc || next != 0 {
		t.Fatalf("second truncation consume returned flag=%v offset=%d", trunc, next)
	}
}

// default; a zero value must never produce an empty read.
func TestEnvMaxReadBounds(t *testing.T) {
	t.Setenv("NABD_MAX_READ", "0")
	if got := envMaxRead(); got != defaultMaxRead() {
		t.Errorf("NABD_MAX_READ=0 → %d, want default %d", got, defaultMaxRead())
	}
	t.Setenv("NABD_MAX_READ", "not-a-number")
	if got := envMaxRead(); got != defaultMaxRead() {
		t.Errorf("NABD_MAX_READ=text → %d, want default %d", got, defaultMaxRead())
	}
	t.Setenv("NABD_MAX_READ", "999999999")
	if got := envMaxRead(); got != defaultMaxRead() {
		t.Errorf("NABD_MAX_READ=huge → %d, want default %d", got, defaultMaxRead())
	}
	t.Setenv("NABD_MAX_READ", "2048")
	if got := envMaxRead(); got != 2048 {
		t.Errorf("NABD_MAX_READ=2048 → %d, want 2048", got)
	}
}

// TestDefaultMaxReadDerivesFromMaxTok: the derived read cap must move when
// NABD_MAX_TOKENS moves — a single derivation, not two hardcoded numbers.
// The shipped default stays at the live-calibrated 3072 until the derived
// value is measured on disk (STEP 1 follow-up).
func TestDefaultMaxReadDerivesFromMaxTok(t *testing.T) {
	// NABD_MAX_READ unset; the derivation recomputes with MaxTok.
	t.Setenv("NABD_MAX_READ", "")
	t.Setenv("NABD_MAX_TOKENS", "")
	at1024 := defaultMaxReadDerived()

	t.Setenv("NABD_MAX_TOKENS", "2048")
	at2048 := defaultMaxReadDerived()

	// Higher output reservation → smaller read cap.
	if at2048 >= at1024 {
		t.Errorf("derivation must shrink when MaxTok grows: MaxTok=1024→%d, MaxTok=2048→%d", at1024, at2048)
	}
	// The shipped default is the conservative live-calibrated value.
	if got := defaultMaxRead(); got != 3072 {
		t.Errorf("defaultMaxRead() = %d, want 3072 (live-calibrated)", got)
	}
	t.Logf("derived at MaxTok=1024: %d bytes; at 2048: %d; shipped default: %d", at1024, at2048, defaultMaxRead())
}
