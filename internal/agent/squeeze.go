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

// truncTailRE matches the truncation tail read_file appends: the range, the
// total, and the explicit next offset, all in lines.
var truncTailRE = regexp.MustCompile(`\[TRUNCATED: read lines (\d+)-(\d+) of (\d+); continue with offset=(\d+)\]`)

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
// ReadRange exposes the truncation-range extractor for its cross-package
// contract test; parsing remains implemented by readRange.
func ReadRange(out string) string { return readRange(out) }

func readRange(out string) string {
	first := -1
	last := -1
	if m := truncTailRE.FindStringSubmatch(out); m != nil {
		// The tail names the range explicitly (start-end); prefer it over
		// re-deriving from numbered lines.
		if n, err := strconv.Atoi(m[1]); err == nil {
			first = n
		}
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
	// Competing tails are a fold-time hazard: consecutive truncated reads
	// of one path leave several "continue with offset=" directives, and a
	// model that follows an older one re-reads an already-covered range.
	// Exactly one live tail per path survives the fold.
	ms = DedupeReadTails(ms)
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
					rs[j].Output = fmt.Sprintf("«read lines %s of %s (content squeezed; do not re-read this range)»", rng, p)
					out[i].ToolResults = rs
					continue
				}
			}
			if len(r.Output) < stubOver {
				continue
			}
			rs[j].Output = fmt.Sprintf("«%d bytes of output removed to save context; re-run the call if needed»", len(r.Output))
		}
		out[i].ToolResults = rs
	}
	return out
}

// DedupeReadTails keeps only the newest truncation tail per path. An older
// tail names a stale next_offset and competes with the live one: a model
// that follows it re-reads an already-covered range and the loop tightens.
// The superseded read result is flattened to the same one-line stub the
// fold uses — the range survives, the stale offset directive does not.
func DedupeReadTails(ms []provider.Message) []provider.Message {
	seen := map[string]bool{} // paths whose newest tail is already kept
	out := make([]provider.Message, len(ms))
	copy(out, ms)
	for i := len(out) - 1; i >= 0; i-- {
		if len(out[i].ToolResults) == 0 {
			continue
		}
		// Paths for read results come from the preceding assistant
		// message's tool_use, matched by ID — same pairing Squeeze keeps.
		paths := map[string]string{}
		if i > 0 {
			for _, tc := range out[i-1].ToolCalls {
				if tc.Name == "read_file" {
					paths[tc.ID] = pathOfArgs(tc.Input)
				}
			}
		}
		rs := make([]provider.ToolResult, len(out[i].ToolResults))
		copy(rs, out[i].ToolResults)
		modified := false
		// Newest first, so within one message the later result wins too.
		for j := len(rs) - 1; j >= 0; j-- {
			if rs[j].IsErr || !isReadResult(rs[j].Output) {
				continue
			}
			if !strings.Contains(rs[j].Output, "[TRUNCATED:") {
				continue
			}
			p := paths[rs[j].ID]
			if p == "" {
				continue
			}
			if seen[p] {
				if rng := readRange(rs[j].Output); rng != "" {
					rs[j].Output = fmt.Sprintf("«read lines %s of %s (content squeezed; do not re-read this range)»", rng, p)
					modified = true
				}
				continue
			}
			seen[p] = true
		}
		if modified {
			out[i].ToolResults = rs
		}
	}
	return out
}

// OfferedOffset is one live "continue with offset=N" directive in a
// request-ready history: the file it points at, the offset it offers, and
// the range the read covered.
type OfferedOffset struct {
	Path   string
	Offset int
	Start  int
	End    int
}

// OfferedOffsets extracts every live truncation tail from a message list,
// oldest first. This is the structural counterpart of "what could the
// model still follow": a count over the actual bytes a request carries,
// not a guess about model behavior.
func OfferedOffsets(ms []provider.Message) []OfferedOffset {
	var out []OfferedOffset
	for i, m := range ms {
		if len(m.ToolResults) == 0 {
			continue
		}
		paths := map[string]string{}
		if i > 0 {
			for _, tc := range ms[i-1].ToolCalls {
				if tc.Name == "read_file" {
					paths[tc.ID] = pathOfArgs(tc.Input)
				}
			}
		}
		for _, r := range m.ToolResults {
			if r.IsErr || !isReadResult(r.Output) {
				continue
			}
			mm := truncTailRE.FindStringSubmatch(r.Output)
			if mm == nil {
				continue
			}
			p := paths[r.ID]
			if p == "" {
				continue
			}
			o := OfferedOffset{Path: p}
			o.Start, _ = strconv.Atoi(mm[1])
			o.End, _ = strconv.Atoi(mm[2])
			o.Offset, _ = strconv.Atoi(mm[4])
			out = append(out, o)
		}
	}
	return out
}

// StaleOffsetsOffered returns the offered offsets that point into territory
// a newer tail of the same path already covers — the structural defect
// behind the offset-repetition loop. A correctly folded request offers
// none: the newest offset is the only one a model can follow.
func StaleOffsetsOffered(ms []provider.Message) []OfferedOffset {
	offered := OfferedOffsets(ms)
	var stale []OfferedOffset
	for i, t := range offered {
		for _, u := range offered[i+1:] {
			if u.Path == t.Path && u.End >= t.Offset {
				stale = append(stale, t)
				break
			}
		}
	}
	return stale
}
