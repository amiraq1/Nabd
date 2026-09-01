package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestReadFileOutputAlwaysValidUTF8: every read_file output must be valid
// UTF-8 no matter where the byte cap or the line clip lands — a cut inside
// a multi-byte rune would corrupt the JSON that carries the tool_result.
func TestReadFileOutputAlwaysValidUTF8(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"ascii", strings.Repeat("a", maxReadBytes+64)},
		{"arabic", strings.Repeat("س", (maxReadBytes+64)/2)}, // 2-byte runes
		{"mixed-arabic-prefix", strings.Repeat("س", 200) + strings.Repeat("x", maxReadBytes)},
		{"emoji", strings.Repeat("😀", (maxReadBytes+64)/4)}, // 4-byte runes
		{"arabic-exact-boundary", strings.Repeat("س", maxReadBytes/2)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, dir := newReg(t)
			path := filepath.Join(dir, "f.txt")
			// 300 lines of the test content — guarantees byte-cap truncation.
			var b strings.Builder
			for i := 0; i < 300; i++ {
				b.WriteString(tc.line + "\n")
			}
			os.WriteFile(path, []byte(b.String()), 0o644)

			raw, _ := json.Marshal(map[string]any{"path": "f.txt"})
			out, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw))
			if err != nil || !ok {
				t.Fatalf("read_file: ok=%v err=%v", ok, err)
			}
			if !utf8.ValidString(out) {
				t.Fatalf("output is NOT valid UTF-8:\n%q", out)
			}
		})
	}
}
