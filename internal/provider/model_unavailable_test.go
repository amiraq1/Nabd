package provider

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode"
)

// Golden constants: the two user-facing formats exactly as the typed error
// shipped them (#227, PR #234), before the provider leg of ADR-0002 decision 4
// moved the wording out of this package. They are the originals, not a
// paraphrase: the provider now renders through its language seam with an ASCII
// English default, and TestModelUnavailableRendersBothSitesByteIdentical
// installs these historical formats through that seam to pin the call-site
// shapes byte for byte. Updating them must be as deliberate as updating any
// other golden value.
const (
	origAnthropicFmt = "الموديل %q غير متاح على هذا الخادم (%d)"
	origOpenAIFmt    = "الموديل %q غير متاح على هذا الخادم (%d).\n%s"
)

// testASCIIOnly reports whether s contains only ASCII runes.
func testASCIIOnly(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

// The provider package carries no user-facing Arabic of its own: the
// model-unavailable message renders from an ASCII English baseline until the
// CLI installs the Arabic catalog through the language seam at startup
// (ADR-0002 decision 4). A library caller, or any path that never runs
// cmd/ag, must see the explicit baseline rather than half of a translation.
func TestModelUnavailableRenderingDefaultsToASCIIBaseline(t *testing.T) {
	got := (&modelUnavailableError{Model: "m", Status: 404, Suffix: ".\nlookup"}).Error()
	if !testASCIIOnly(got) {
		t.Fatalf("model-unavailable default rendering is not ASCII: %q", got)
	}
}

// A model-unavailable answer (404/410) is classified by TYPE. Before this, the
// retry decision read the wording of the message with strings.Contains, so
// re-wording, re-accenting, or wrapping it silently changed whether the
// provider was retried.
func TestModelUnavailableIsTypedAndNotRetried(t *testing.T) {
	err := &modelUnavailableError{Model: "claude-x", Status: 404}
	if transient(err) {
		t.Fatal("a model-unavailable answer must not be retried")
	}
	var mue *modelUnavailableError
	if !errors.As(error(err), &mue) {
		t.Fatal("model-unavailable must be discoverable with errors.As")
	}
	if mue.Status != 404 || mue.Model != "claude-x" {
		t.Fatalf("fields must survive errors.As, got %+v", *mue)
	}
}

// The verdict must not depend on the prose the message happens to carry.
func TestModelUnavailableRetryDecisionIgnoresWording(t *testing.T) {
	for _, suffix := range []string{"", ".\n", ".\nmodel not found on this server", ".\nخطأ في الموديل"} {
		e := &modelUnavailableError{Model: "m", Status: 410, Suffix: suffix}
		if transient(e) {
			t.Fatalf("retry decision must not depend on message wording (suffix=%q)", suffix)
		}
	}
}

// historicalArabicRendering reproduces the historical runtime template
// exactly: the two verbs render model and status, and the suffix is appended
// verbatim (the empty, ".\n", and ".\n"+hint shapes all derive from it).
func historicalArabicRendering(model string, status int, suffix string) string {
	return fmt.Sprintf(origAnthropicFmt, model, status) + suffix
}

// Error() must render through the installed seam, not through a copy of the
// wording: the cmd/ag catalog test pins what the CLI installs, and this pins
// that Error() consults whatever has been installed.
func TestModelUnavailableErrorReadsTheSeam(t *testing.T) {
	SetModelUnavailableMessage(func(model string, status int, suffix string) string {
		return fmt.Sprintf("seam(%s|%d|%s)", model, status, suffix)
	})
	t.Cleanup(func() { SetModelUnavailableMessage(defaultModelUnavailableRendering) })

	got := (&modelUnavailableError{Model: "m", Status: 410, Suffix: "!s"}).Error()
	if want := "seam(m|410|!s)"; got != want {
		t.Fatalf("Error() must read the seam:\n got %q\nwant %q", got, want)
	}
	// The pre-rendered Message branch bypasses the seam by design.
	if got := (&modelUnavailableError{Model: "m", Status: 400, Message: "raw body"}).Error(); got != "raw body" {
		t.Fatalf("pre-rendered Message must bypass the seam, got %q", got)
	}
}

// Router.classifyError falls back to matching a route error's rendered text
// against isModelNotFound for errors that are not *httpError, and a
// modelUnavailableError is exactly such an error. Neither the ASCII baseline
// nor the installed Arabic catalog may read as a model-not-found body, or a
// wording edit would silently move the refusal onto the router's 400 text
// path.
func TestModelUnavailableTextNeverReadsAsModelNotFound(t *testing.T) {
	renderings := map[string]func(model string, status int, suffix string) string{
		"ascii baseline":   defaultModelUnavailableRendering,
		"installed arabic": historicalArabicRendering,
	}
	suffix := ".\n" + modelLookupHint("openai", "gpt-x")
	for name, render := range renderings {
		for _, status := range []int{404, 410} {
			text := render("gpt-x", status, suffix)
			if isModelNotFound(400, text) {
				t.Fatalf("%s: rendered text reads as a model-not-found body: %q", name, text)
			}
			if strings.Contains(text, "400") {
				t.Fatalf("%s: rendered text contains \"400\" and would take the router's text 400 branch: %q", name, text)
			}
		}
	}
}

// Neither user-facing message may change by a byte, including the body-based
// OpenAI heuristic and the empty-hint shape the status branch could in
// principle produce. The historical formats are installed through the same
// seam the CLI uses, so this also pins that the seam reproduces them exactly.
func TestModelUnavailableRendersBothSitesByteIdentical(t *testing.T) {
	SetModelUnavailableMessage(historicalArabicRendering)
	t.Cleanup(func() { SetModelUnavailableMessage(defaultModelUnavailableRendering) })

	hint := modelLookupHint("openai", "gpt-x")

	wantHeuristic := "model not found\n" + hint
	gotHeuristic := (&modelUnavailableError{
		Model:   "gpt-x",
		Status:  400,
		Message: wantHeuristic,
	}).Error()
	if gotHeuristic != wantHeuristic {
		t.Fatalf("OpenAI heuristic message changed:\n got %q\nwant %q", gotHeuristic, wantHeuristic)
	}

	wantOpenAI := fmt.Sprintf(origOpenAIFmt, "gpt-x", 404, hint)
	gotOpenAI := (&modelUnavailableError{Model: "gpt-x", Status: 404, Suffix: ".\n" + hint}).Error()
	if gotOpenAI != wantOpenAI {
		t.Fatalf("openai message changed:\n got %q\nwant %q", gotOpenAI, wantOpenAI)
	}

	wantEmpty := fmt.Sprintf(origOpenAIFmt, "gpt-x", 404, "")
	gotEmpty := (&modelUnavailableError{Model: "gpt-x", Status: 404, Suffix: ".\n"}).Error()
	if gotEmpty != wantEmpty {
		t.Fatalf("openai empty-hint message changed:\n got %q\nwant %q", gotEmpty, wantEmpty)
	}

	wantAnthropic := fmt.Sprintf(origAnthropicFmt, "claude-x", 410)
	gotAnthropic := (&modelUnavailableError{Model: "claude-x", Status: 410}).Error()
	if gotAnthropic != wantAnthropic {
		t.Fatalf("anthropic message changed:\n got %q\nwant %q", gotAnthropic, wantAnthropic)
	}
}

// The body heuristic must carry the same typed retry verdict as the 404/410
// status branch. This is the exact gap reported in #227.
func TestModelUnavailableHeuristicIsTypedAndNotRetried(t *testing.T) {
	err := &modelUnavailableError{
		Model:   "gpt-x",
		Status:  400,
		Message: "model not found\nlookup hint",
	}
	if transient(err) {
		t.Fatal("the body-based model-unavailable answer must not be retried")
	}
	var mue *modelUnavailableError
	if !errors.As(error(err), &mue) {
		t.Fatal("the body-based model-unavailable answer must remain typed")
	}
	if mue.Message != err.Message {
		t.Fatalf("message lost through typed classification: got %q want %q", mue.Message, err.Message)
	}
}

// Wrapping was one of the stated motivations for the typed error, so it is
// pinned: errors.As must see through a %w wrap and the verdict must hold.
func TestModelUnavailableSurvivesWrapping(t *testing.T) {
	base := &modelUnavailableError{Model: "m", Status: 404}
	wrapped := fmt.Errorf("provider %q: %w", "openai", base)
	if transient(wrapped) {
		t.Fatal("a wrapped model-unavailable answer must not be retried")
	}
	var mue *modelUnavailableError
	if !errors.As(wrapped, &mue) {
		t.Fatal("errors.As must find the type through a %w wrap")
	}
	if mue.Model != "m" || mue.Status != 404 {
		t.Fatalf("fields lost through wrapping: %+v", *mue)
	}
}

// The pre-existing httpError ladder is unchanged by the new type.
func TestHTTPErrorRetryLadderUnchanged(t *testing.T) {
	for _, tc := range []struct {
		status    int
		retryable bool
	}{
		{400, false}, {401, false}, {404, false}, {410, false},
		{408, true}, {409, true}, {429, true}, {500, true}, {503, true},
	} {
		if got := transient(&httpError{Status: tc.status, Body: "x"}); got != tc.retryable {
			t.Fatalf("http %d: transient=%v, want %v", tc.status, got, tc.retryable)
		}
	}
}
