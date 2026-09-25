package provider

import (
	"errors"
	"fmt"
	"testing"
)

// Golden constants: the two user-facing formats exactly as they stood on master
// before the typed error replaced them. They are the originals, not a
// paraphrase. After this change merges there is no longer any in-tree source to
// extract them from, so updating them must be as deliberate as updating any
// other golden value.
const (
	origAnthropicFmt = "الموديل %q غير متاح على هذا الخادم (%d)"
	origOpenAIFmt    = "الموديل %q غير متاح على هذا الخادم (%d).\n%s"
)

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

// Neither user-facing message may change by a byte, including the empty-hint
// shape the OpenAI literal could in principle produce.
func TestModelUnavailableRendersBothSitesByteIdentical(t *testing.T) {
	hint := modelLookupHint("openai", "gpt-x")

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
