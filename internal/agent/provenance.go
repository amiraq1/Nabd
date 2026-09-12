package agent

import (
	"regexp"
	"strings"
)

var readGutterLine = regexp.MustCompile(`^\s*\d+\|`)

// UncorroboratedReadLines returns assistant text lines that look like numbered
// read_file output but were not returned by a read_file ToolEnd in the same
// user round. It compares event provenance, not wording or prompt phrases.
func UncorroboratedReadLines(evs []Event) []string {
	var out []string
	var toolOutput []string
	var assistant strings.Builder
	flush := func() {
		for _, line := range strings.Split(assistant.String(), "\n") {
			if !readGutterLine.MatchString(line) {
				continue
			}
			corroborated := false
			for _, got := range toolOutput {
				if strings.Contains(got, line) {
					corroborated = true
					break
				}
			}
			if !corroborated {
				out = append(out, line)
			}
		}
		toolOutput = nil
		assistant.Reset()
	}
	for _, e := range Live(evs) {
		switch e.Type {
		case UserMsg:
			flush()
		case TextDelta:
			assistant.WriteString(e.Text)
		case ToolEnd:
			if e.Call != nil && e.Call.Name == "read_file" && e.Call.OK {
				toolOutput = append(toolOutput, e.Call.Output)
			}
		}
	}
	flush()
	return out
}
