package provider

import (
	"fmt"
	"sync"
)

// ─────────────────────────────────────────────────────────────────────────────
// Provider error wording: ASCII defaults, one install point at the CLI.
//
// The last hand-written Arabic in this package moved out with the provider leg
// of ADR-0002 decision 4 (issue #223). The two messages below are the
// package's user-visible text, and they render through this seam so the wording
// can live in the sanctioned CLI catalog (cmd/ag/errors.go) while the provider
// keeps an explicit ASCII English baseline.
//
// The seam lives here rather than at a display boundary because both messages
// are already text before any display boundary is reached: the router folds a
// route's error string into ProviderError.Body and RouterExhaustedError.Error(),
// and the agent journals err.Error() into the RunError event. Installing the
// wording at the point of rendering is what keeps the feed, the headless stderr
// line, the journal, and the router details byte-identical to the shapes this
// move replaced.
//
// A caller that never runs the CLI (a library embedding, a unit test) sees the
// ASCII baseline rather than a partial translation; installing is a startup
// decision made once in main, before any provider answer can arrive.
// ─────────────────────────────────────────────────────────────────────────────

const (
	// defaultModelUnavailableFormat mirrors the historical message shape; the
	// CLI installs the Arabic template over it with SetModelUnavailableMessage.
	defaultModelUnavailableFormat = "model %q is unavailable on this server (%d)%s"
	// defaultFirstByteTimeoutMessage is raised when the server accepts a
	// request but sends no first byte within the 25-second watch window.
	defaultFirstByteTimeoutMessage = "provider did not start responding within 25 seconds - try another NABD_MODEL"
)

// defaultModelUnavailableRendering is the ASCII baseline renderer. It is kept
// as a named function so tests and the setter can restore it.
func defaultModelUnavailableRendering(model string, status int, suffix string) string {
	return fmt.Sprintf(defaultModelUnavailableFormat, model, status, suffix)
}

var (
	languageMu              sync.RWMutex
	modelUnavailableMessage = defaultModelUnavailableRendering
	firstByteTimeoutMessage = defaultFirstByteTimeoutMessage
)

// SetModelUnavailableMessage replaces the rendering of a model-unavailable
// refusal. The CLI installs the Arabic template here at startup; a nil
// renderer is ignored. The renderer receives the same three values the
// historical format received: model, HTTP status, and the verbatim suffix.
func SetModelUnavailableMessage(render func(model string, status int, suffix string) string) {
	if render == nil {
		return
	}
	languageMu.Lock()
	defer languageMu.Unlock()
	modelUnavailableMessage = render
}

// SetFirstByteTimeoutMessage replaces the first-byte timeout text. An empty
// argument keeps the current value, so an install that lost its catalog cannot
// blank the message.
func SetFirstByteTimeoutMessage(message string) {
	if message == "" {
		return
	}
	languageMu.Lock()
	defer languageMu.Unlock()
	firstByteTimeoutMessage = message
}

// ModelUnavailableMessage renders a model-unavailable refusal with the
// currently installed wording.
func ModelUnavailableMessage(model string, status int, suffix string) string {
	languageMu.RLock()
	render := modelUnavailableMessage
	languageMu.RUnlock()
	return render(model, status, suffix)
}

// FirstByteTimeoutMessage returns the currently installed first-byte timeout
// text.
func FirstByteTimeoutMessage() string {
	languageMu.RLock()
	defer languageMu.RUnlock()
	return firstByteTimeoutMessage
}
