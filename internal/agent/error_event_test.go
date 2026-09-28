package agent

import (
	"errors"
	"nabd/internal/event"
	"testing"

	"nabd/internal/endpoint"
)

func TestRunErrorEventCarriesTypedCode(t *testing.T) {
	if got := RunErrorEvent(event.ErrSpendBudget); got.ErrorCode != string(event.ErrCodeBudget) {
		t.Fatalf("code = %q, want %q", got.ErrorCode, event.ErrCodeBudget)
	}
	if got := RunErrorEvent(event.ErrMaxTurns); got.ErrorCode != string(event.ErrCodeMaxTurns) {
		t.Fatalf("code = %q, want %q", got.ErrorCode, event.ErrCodeMaxTurns)
	}
}

func TestPersistErrorCarriesJournalPath(t *testing.T) {
	err := event.NewPersistError(errors.New("disk full"), "/tmp/session.jsonl")
	if ErrorCodeOf(err) != event.ErrCodePersist || event.JournalPathOf(err) != "/tmp/session.jsonl" {
		t.Fatalf("classification = %q path=%q", ErrorCodeOf(err), event.JournalPathOf(err))
	}
}

func TestEndpointRefusedTwoLayerWrapClassified(t *testing.T) {
	// Layer 1: CheckBaseURL wraps ErrEndpointRefused
	layer1 := errors.Join(errors.New("baseURL http://127.0.0.1:8118 uses scheme http"), endpoint.ErrEndpointRefused)
	// Layer 2: proxyFromEnv / transport wraps layer 1
	layer2 := errors.Join(errors.New("Post https://api.groq.com/openai/v1/chat/completions: proxy endpoint refused"), layer1)

	if got := ErrorCodeOf(layer2); got != event.ErrCodeEndpointRefused {
		t.Fatalf("ErrorCodeOf(layer2) = %q, want %q", got, event.ErrCodeEndpointRefused)
	}
	if got := RunErrorEvent(layer2).ErrorCode; got != string(event.ErrCodeEndpointRefused) {
		t.Fatalf("RunErrorEvent(layer2).ErrorCode = %q, want %q", got, event.ErrCodeEndpointRefused)
	}
}

func TestGenericUnrelatedErrorRemainsUnknown(t *testing.T) {
	generic := errors.New("connection reset by peer")
	if got := ErrorCodeOf(generic); got != event.ErrCodeUnknown {
		t.Fatalf("ErrorCodeOf(generic) = %q, want %q", got, event.ErrCodeUnknown)
	}

	wrapped := errors.Join(errors.New("outer wrap"), errors.New("inner failure"))
	if got := ErrorCodeOf(wrapped); got != event.ErrCodeUnknown {
		t.Fatalf("ErrorCodeOf(wrapped) = %q, want %q", got, event.ErrCodeUnknown)
	}
}
