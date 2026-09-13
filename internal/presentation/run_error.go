package presentation

import (
	"fmt"
	"strings"

	"nabd/internal/agent"
)

// maxRunErrorDetails caps how many per-route detail lines reach the screen.
// The journal keeps all of them; the feed is a phone screen, and an error
// body is attacker-influenced input (provider responses, tool output), so it
// must not be able to push the rest of the conversation out of view.
const maxRunErrorDetails = 6

// RunErrorView is the user-visible form of a terminal failure.
//
// It separates the three things a reader needs and which used to be merged
// into one opaque line: what failed (Headline), why each route failed
// (Details, written by the router into the error body), and what to do next
// (Hint, derived from the journaled ErrorCode).
type RunErrorView struct {
	Headline string
	Details  []string
	Hint     string
}

// FormatRunError builds the visible failure from a RunError event.
//
// Safety: every line passes through cleanField, i.e. display sanitization
// with redaction enabled, so a provider error body that echoes a key cannot
// print it. Output lines are single logical lines with no terminal controls.
func FormatRunError(e agent.Event) RunErrorView {
	var kept []string
	for _, line := range strings.Split(e.Err, "\n") {
		if clean := cleanField(line, ""); clean != "" {
			kept = append(kept, clean)
		}
	}

	var v RunErrorView
	if len(kept) == 0 {
		// A failure with no message is still a failure: never render an
		// empty row, or the run looks like it simply stopped.
		v.Headline = "run failed"
	} else {
		v.Headline = kept[0]
		rest := kept[1:]
		if len(rest) > maxRunErrorDetails {
			v.Details = append(v.Details, rest[:maxRunErrorDetails]...)
			v.Details = append(v.Details, fmt.Sprintf("… %d more in the journal", len(rest)-maxRunErrorDetails))
		} else {
			v.Details = append(v.Details, rest...)
		}
	}

	code := ErrorCode(cleanField(e.ErrorCode, ""))
	if code == "" {
		code = ErrCodeUnknown
	}
	// "unknown" is not information; printing it would only make the row
	// longer on the narrow screens where the row matters most.
	if code != ErrCodeUnknown {
		v.Headline += " · " + string(code)
	}
	if e.Code > 0 {
		v.Headline += fmt.Sprintf(" · http %d", e.Code)
	}
	v.Hint = runErrorHint(code)
	return v
}

// runErrorHint maps a machine code to the one next step that resolves it.
// Codes with no user-side remedy (canceled, unknown) deliberately get none:
// a hint that says nothing trains the reader to ignore hints.
func runErrorHint(code ErrorCode) string {
	switch code {
	case ErrCodeProviderAuth:
		return "check the provider key in ~/.ag/config, then retry"
	case ErrCodeBudget:
		return "rate-limit budget spent · wait out the reported delay, or set NABD_ROUTER_RETRY_AFTER_WAIT"
	case ErrCodePersist:
		return "the journal could not be written · check free space and permissions on ~/.ag"
	default:
		return ""
	}
}

// Lines returns the failure as ordered display lines: headline, route
// details, then the hint.
func (v RunErrorView) Lines() []string {
	out := make([]string, 0, len(v.Details)+2)
	out = append(out, v.Headline)
	out = append(out, v.Details...)
	if v.Hint != "" {
		out = append(out, v.Hint)
	}
	return out
}

func (v RunErrorView) String() string { return strings.Join(v.Lines(), "\n") }
