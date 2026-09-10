package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/config"
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
	if !strings.Contains(out, "continue with offset=") {
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

// TestEnvMaxReadRoutesThroughConfig proves the read limit reads through the
// application configuration layer (config.Get), so a value set in
// ~/.ag/config is honored and takes precedence over the environment — the same
// contract every other limit uses. The package-level var is computed once at
// init with the default; these tests exercise the functions directly so they
// remain testable.
func TestEnvMaxReadRoutesThroughConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")

	// Env-only (no config file): value is honored via config.Get's env fallback.
	t.Setenv("NABD_CONFIG", path)
	t.Setenv("NABD_MAX_READ", "2048")
	config.ResetForTest()
	if got := envMaxRead(); got != 2048 {
		t.Fatalf("env NABD_MAX_READ=2048 → %d, want 2048", got)
	}

	// Config file value honored.
	os.WriteFile(path, []byte("NABD_MAX_READ=7777\n"), 0o600)
	config.ResetForTest()
	if got := envMaxRead(); got != 7777 {
		t.Fatalf("file NABD_MAX_READ=7777 → %d, want 7777", got)
	}

	// Config file takes precedence over the environment.
	t.Setenv("NABD_MAX_READ", "1111")
	config.ResetForTest()
	if got := envMaxRead(); got != 7777 {
		t.Fatalf("file should win over env: got %d, want 7777", got)
	}

	// Malformed file value falls back to default.
	os.WriteFile(path, []byte("NABD_MAX_READ=not-a-number\n"), 0o600)
	config.ResetForTest()
	if got := envMaxRead(); got != defaultMaxRead() {
		t.Fatalf("malformed → %d, want default %d", got, defaultMaxRead())
	}

	// Out-of-range (too large) falls back to default.
	os.WriteFile(path, []byte("NABD_MAX_READ=999999999\n"), 0o600)
	config.ResetForTest()
	if got := envMaxRead(); got != defaultMaxRead() {
		t.Fatalf("out-of-range → %d, want default %d", got, defaultMaxRead())
	}

	// Zero/negative fall back to default.
	os.WriteFile(path, []byte("NABD_MAX_READ=0\n"), 0o600)
	config.ResetForTest()
	if got := envMaxRead(); got != defaultMaxRead() {
		t.Fatalf("zero → %d, want default %d", got, defaultMaxRead())
	}

	// Missing value (neither file nor env) uses the documented default.
	os.Remove(path)
	t.Setenv("NABD_MAX_READ", "")
	config.ResetForTest()
	if got := envMaxRead(); got != defaultMaxRead() {
		t.Fatalf("unset → %d, want default %d", got, defaultMaxRead())
	}
}

// TestReadCapPinsProviderIndependence_NBD401 pins the Router-safety property
// this stage requires. The read cap must not be derived from the provider's
// name: Router.Name() is a composite display string (built as
// "router/<provider>:<model>→<provider>:<model>", truncated at 200 bytes), so
// any policy that parsed a provider out of it would mis-key itself for exactly
// the multi-provider case. The cap is a pure function of NABD_MAX_READ, so
// changing which provider is selected cannot move it.
//
// This is a deliberate pin on a state that is not good in itself: the read cap
// is a fixed number precisely because the cumulative cost it stands for has
// not been replaced by a budget-aware policy yet (docs/TECH_DEBT.md,
// READ_CAP_TURN_COST). Keying it to the provider would look like a fix and
// would be worse, which is why the failure message says so.
func TestReadCapPinsProviderIndependence_NBD401(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	t.Setenv("NABD_CONFIG", cfg)

	// A composite router route is the case a name-parsing policy would break;
	// the single-provider selections are the contrast.
	selections := []string{
		"",
		"NABD_PROVIDER=groq\n",
		"NABD_PROVIDER=anthropic\n",
		"NABD_PROVIDER=router\nNABD_ROUTES=groq:llama→openrouter:qwen\n",
	}
	for i, sel := range selections {
		if err := os.WriteFile(cfg, []byte("NABD_MAX_READ=4096\n"+sel), 0o600); err != nil {
			t.Fatal(err)
		}
		config.ResetForTest()
		if got := envMaxRead(); got != 4096 {
			t.Fatalf("selection %d (%q): envMaxRead()=%d, want 4096 — provider selection leaked into the read cap. "+
				"The cap is deliberately provider-blind; see READ_CAP_TURN_COST in docs/TECH_DEBT.md for why the fixed value exists and what would replace it.",
				i, sel, got)
		}
	}

	// NABD_MAX_READ unset: the cap must be the shipped constant, not something
	// that varies with who is selected.
	if err := os.WriteFile(cfg, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	for i, sel := range selections {
		if err := os.WriteFile(cfg, []byte(sel), 0o600); err != nil {
			t.Fatal(err)
		}
		config.ResetForTest()
		if got := envMaxRead(); got != defaultMaxRead() {
			t.Fatalf("selection %d (%q): unset NABD_MAX_READ gave %d, want the shipped %d; "+
				"the cap must not depend on the provider; see READ_CAP_TURN_COST in docs/TECH_DEBT.md",
				i, sel, got, defaultMaxRead())
		}
	}
}
