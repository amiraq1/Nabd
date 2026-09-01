package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLongSingleLineAdvancesOffset(t *testing.T) {
	r, dir := newReg(t)
	for _, tc := range []struct {
		name string
		line string
	}{
		{name: "ascii", line: strings.Repeat("a", maxReadBytes+128)},
		{name: "arabic", line: strings.Repeat("س", maxReadBytes+128)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".txt")
			if err := os.WriteFile(path, []byte(tc.line+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]any{"path": tc.name + ".txt"})
			out, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw))
			if err != nil || !ok {
				t.Fatalf("read_file: ok=%v err=%v", ok, err)
			}
			trunc, next := r.ConsumeTruncated()
			t.Logf("maxReadBytes=%d input_bytes=%d truncated=%v next_offset=%d offset=1 output_bytes=%d output=%q", maxReadBytes, len(tc.line)+1, trunc, next, len(out), out)
			if !trunc {
				t.Fatal("long single line must be marked truncated")
			}
			if next <= 1 {
				t.Fatalf("next_offset=%d, want > offset=1", next)
			}
			if next != 2 {
				t.Fatalf("next_offset=%d, want 2 after the only line", next)
			}
			if !strings.Contains(out, "LINE_TRUNCATED") {
				t.Fatalf("long line must carry an explicit line-truncation marker: %q", out)
			}
		})
	}
}
