// Package agent: squeeze.go drops stale tool output before paying a model to
// summarise it. Recent rounds stay verbatim; older ones keep their identity
// and lose their body. Errors are never squeezed: they are short and they
// carry the lesson that stops the model repeating itself.
package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"nabd/internal/provider"
)

const (
	KeepFullRounds = 3
	stubOver       = 400 // characters below this cost less than the stub
)

// readLineRE matches the leading line number of a read_file result line.
var readLineRE = regexp.MustCompile(`^(\d+)\|`)

// truncTailRE matches the truncation tail read_file appends: the range and
// the explicit next offset, all in lines.
var truncTailRE = regexp.MustCompile(`\[TRUNCATED: قُرئت الأسطر (\d+)-(\d+) من (\d+)؛ للمتابعة استخدم offset=\d+\]`)

// isReadResult reports whether a tool result came from read_file: its body
// is line-numbered output (`N|...`) — nothing else in the registry emits
// that shape.
func isReadResult(out string) bool {
	for _, ln := range strings.Split(out, "\n") {
		if ln == "" {
			continue
		}
		if strings.HasPrefix(ln, "[TRUNCATED") {
			continue
		}
		return readLineRE.MatchString(ln)
	}
	return false
}

// readRange summarises what a read result covered: "1-29" from the first
// and last numbered lines, or the explicit range from the truncation tail.
func readRange(out string) string {
	first := -1
	last := -1
	if m := truncTailRE.FindStringSubmatch(out); m != nil {
		// The tail names the range explicitly (start-end); prefer it over
		// re-deriving from numbered lines.
		if n, err := strconv.Atoi(m[2]); err == nil {
			last = n
		}
	}
	for _, ln := range strings.Split(out, "\n") {
		if m := readLineRE.FindStringSubmatch(ln); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				if first < 0 || n < first {
					first = n
				}
				if n > last {
					last = n
				}
			}
		}
	}
	if first < 0 {
		return ""
	}
	if last < first {
		last = first
	}
	return fmt.Sprintf("%d-%d", first, last)
}

// pathOfArgs pulls the 'path' key out of a tool call's raw JSON.
func pathOfArgs(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return ""
	}
	return a.Path
}

func Squeeze(ms []provider.Message, keepRounds int) []provider.Message {
	rounds, cut := 0, -1
	for i := len(ms) - 1; i >= 0; i-- {
		if ms[i].Role != provider.User {
			continue
		}
		rounds++
		if rounds > keepRounds {
			cut = i
			break
		}
	}
	if cut < 0 {
		return ms
	}
	out := make([]provider.Message, len(ms))
	copy(out, ms)
	for i := 0; i < cut; i++ {
		if len(out[i].ToolResults) == 0 {
			continue
		}
		rs := make([]provider.ToolResult, len(out[i].ToolResults))
		copy(rs, out[i].ToolResults)
		// Paths for read stubs come from the preceding assistant message's
		// tool_use, matched by ID — the pairing must survive the squeeze.
		paths := map[string]string{}
		if i > 0 {
			for _, tc := range out[i-1].ToolCalls {
				if tc.Name == "read_file" {
					paths[tc.ID] = pathOfArgs(tc.Input)
				}
			}
		}
		for j, r := range rs {
			if r.IsErr {
				continue
			}
			// A stale read result becomes one line: the range the model
			// actually saw, and how to get more. The tool_use stays, so the
			// pairing is intact and the model knows the file was read.
			// Truncated reads are always stubbed (a partial read is a fact,
			// however short); complete reads follow the size threshold.
			if isReadResult(r.Output) {
				rng := readRange(r.Output)
				p := paths[r.ID]
				if p != "" && rng != "" && (strings.Contains(r.Output, "[TRUNCATED") || len(r.Output) >= stubOver) {
					// The stub records the range and explicitly tells the
					// model not to re-read it: the content is gone from
					// context, and re-reading would only re-truncate and
					// re-accumulate. Preventing the loop beats inviting it.
					rs[j].Output = fmt.Sprintf("«قرأتُ الأسطر %s من %s (المحتوى مضغوط؛ لا تعد قراءة هذا النطاق)»", rng, p)
					out[i].ToolResults = rs
					continue
				}
			}
			if len(r.Output) < stubOver {
				continue
			}
			rs[j].Output = fmt.Sprintf("«%d بايت من المخرَج أُزيلت لتوفير السياق؛ أعد النداء إن احتجتها»", len(r.Output))
		}
		out[i].ToolResults = rs
	}
	return out
}
