package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadSinglePassSourceShape is the source-shape half of the TECH_DEBT
// guard READ_SINGLE_PASS_HASH_RENDER_EQUIVALENCE: the single io.ReadAll in
// read.go is what guarantees the ReadCredit hash covers exactly the bytes the
// model sees. A refactor re-introducing seek-based splitting (or any second
// read pass) must fail this test, because the two views of the file could
// then silently diverge. See docs/reports/pr139_audit.md fix #4.
func TestReadSinglePassSourceShape(t *testing.T) {
	src, err := os.ReadFile("read.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	if n := strings.Count(body, "io.ReadAll("); n != 1 {
		t.Errorf("read.go has %d io.ReadAll calls, want exactly 1: hashing and rendering must share one read pass", n)
	}
	for _, banned := range []string{".Seek(", ".ReadAt("} {
		if strings.Contains(body, banned) {
			t.Errorf("read.go calls %s; a second positional read can desync the hash from the rendered bytes", banned)
		}
	}
}

// TestReadCreditHashMatchesModelVisibleSource is the end-to-end half of the
// guard: the hash stored in ReadCredit must equal sha256 of the exact bytes
// that produced the rendered, model-visible output. It pins both directions:
// the hash matches the source on disk (one read pass, no stale buffer), and
// every rendered line is drawn from those same hashed bytes.
func TestReadCreditHashMatchesModelVisibleSource(t *testing.T) {
	r, dir := newReg(t)
	ctx := context.Background()

	content := "alpha line\nbeta line\ngamma line\n"
	path := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, _ := json.Marshal(map[string]any{"path": "target.txt"})
	out, err := r.RunDetailed(ctx, "read_file", raw)
	if err != nil || !out.OK {
		t.Fatalf("read_file: ok=%v err=%v", out.OK, err)
	}

	// The hashed bytes are the file bytes, read once from the descriptor.
	disk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(disk)
	if got := out.ReadCredit.Hash; got != hex.EncodeToString(want[:]) {
		t.Errorf("ReadCredit.Hash = %q, want sha256(model-visible source) = %q", got, hex.EncodeToString(want[:]))
	}

	// Every rendered numbered line must come from the hashed bytes: the model
	// saw nothing the hash does not cover.
	for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		if !strings.Contains(out.Text, "|"+line+"\n") && !strings.HasSuffix(out.Text, "|"+line) {
			t.Errorf("rendered output is missing source line %q; render and hash diverged", line)
		}
	}
	if out.ReadCredit.LinesRead != 3 {
		t.Errorf("ReadCredit.LinesRead = %d, want 3", out.ReadCredit.LinesRead)
	}
}
