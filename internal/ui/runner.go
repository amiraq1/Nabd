package ui

import "context"

// runner.go owns the symbols the interactive surfaces share with the agent
// loop. They were declared in chat.go; they moved here when the Chat surface
// was retired (ADR-0001, v1.7.0) so that Feed does not depend on a file whose
// deletion would silently break it.
//
// Nothing in this file is Chat-specific: every declaration below has at least
// one non-Chat consumer, and each is listed with that consumer so the next
// deletion cannot repeat the mistake of removing a shared symbol with a
// surface.

// Runner is the loop, seen from the UI: one message in, events out.
//
// Consumers: Feed (feed.go field `runner` and SetRunner) and the test adapters
// that implement it without touching a provider.
type Runner interface {
	Run(ctx context.Context, text string) error
}

// doneMsg is the tea message a finished run produces: it lands when the runner
// goroutine returns, not when the last event is drawn, so it is what clears the
// busy send gate.
//
// Consumers: Feed (feed.go Update and feed_input.go's run command) and the
// feed test suite. It is shared, not Chat-owned.
type doneMsg struct{ err error }

// errSummary formats a runtime error for the UI status bar while ensuring no
// non-ASCII runes outside AllowedUISymbols leak into the interface.
//
// Consumers: Feed (feed.go run-failure notice) and path_picker_wire.go.
func errSummary(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	for _, r := range s {
		if r >= 128 && !AllowedUISymbols[r] {
			return "execution failed"
		}
	}
	return s
}
