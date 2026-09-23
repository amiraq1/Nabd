package presentation

import (
	"fmt"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/endpoint"
)

func TestLegacyRunErrorProjectsAsUnknown(t *testing.T) {
	items, err := NewProjector().Build([]agent.Event{{Seq: 1, Type: agent.RunError, Err: "old failure"}})
	if err != nil || len(items) != 1 || items[0].Error == nil {
		t.Fatalf("projection failed: items=%#v err=%v", items, err)
	}
	card := items[0].Error
	if card.Code != agent.ErrCodeUnknown || card.Retryable || card.RetryScope != RetryNone {
		t.Fatalf("legacy card = %#v", card)
	}
}

func TestProviderTemporaryRetryScope(t *testing.T) {
	card := NewErrorCard(agent.ErrProviderTemporary, "temporary", "")
	if !card.Retryable || card.RetryScope != RetryProviderTurn {
		t.Fatalf("card = %#v", card)
	}
}

func TestProviderAuthDoesNotBlindRetry(t *testing.T) {
	card := NewErrorCard(agent.ErrCodeProviderAuth, "unauthorized", "")
	if card.Retryable || card.RetryScope != RetryNone {
		t.Fatalf("card = %#v", card)
	}
}

// TestErrorCardCarriesWaitSeconds keeps the router's retry-after as a field on
// the card. The message text that also contains the number is truncated to the
// terminal width above 40 columns and hidden below it, so a card without the
// field loses the only actionable number on a phone terminal.
func TestErrorCardCarriesWaitSeconds(t *testing.T) {
	items, err := NewProjector().Build([]agent.Event{
		{Seq: 1, Type: agent.RunError, Err: "all 2 route(s) exhausted", ErrorCode: "provider_temporary", RetryAfter: 20},
	})
	if err != nil || len(items) != 1 || items[0].Error == nil {
		t.Fatalf("projection failed: items=%#v err=%v", items, err)
	}
	if got := items[0].Error.WaitSeconds; got != 20 {
		t.Fatalf("WaitSeconds = %v, want 20", got)
	}

	// A run that reported no retry-after states no wait: 0 means "none", not
	// "wait zero seconds".
	items, err = NewProjector().Build([]agent.Event{
		{Seq: 1, Type: agent.RunError, Err: "auth failed", ErrorCode: "provider_auth"},
	})
	if err != nil || len(items) != 1 || items[0].Error == nil {
		t.Fatalf("projection failed: items=%#v err=%v", items, err)
	}
	if got := items[0].Error.WaitSeconds; got != 0 {
		t.Fatalf("WaitSeconds = %v, want 0", got)
	}
}

// TestCrossPackageEndpointRefusedRemedyFlow exercises the complete path across package
// boundaries: an error wrapping endpoint.ErrEndpointRefused is classified by agent into
// agent.RunErrorEvent, projected/converted into presentation.ErrorCard, and verifies
// that card.Remedy is populated and specifies NABD_ENDPOINT_POLICY.
// If the error code string diverges between agent and presentation, this test fails.
func TestCrossPackageEndpointRefusedRemedyFlow(t *testing.T) {
	// 1. Create a wrapped endpoint error (2 layers of %w wrapping)
	baseErr := fmt.Errorf("baseURL http://127.0.0.1:8118 uses scheme http: %w", endpoint.ErrEndpointRefused)
	fullErr := fmt.Errorf("Post https://api.groq.com/openai/v1/chat/completions: proxy endpoint refused: %w", baseErr)

	// 2. Classify in agent package via RunErrorEvent (simulating what the agent Loop does on error)
	ev := agent.RunErrorEvent(fullErr)
	if ev.ErrorCode != "endpoint_refused" {
		t.Fatalf("agent.RunErrorEvent classified code = %q, want %q", ev.ErrorCode, "endpoint_refused")
	}

	// 3. Convert to presentation.ErrorCard
	cardFromEvent := ErrorCardFromEvent(ev)
	if cardFromEvent.Remedy != RemedyEndpointRefused {
		t.Fatalf("cardFromEvent.Remedy = %q, want %q", cardFromEvent.Remedy, RemedyEndpointRefused)
	}

	cardFromErr := ErrorCardFromError(fullErr)
	if cardFromErr.Remedy != RemedyEndpointRefused {
		t.Fatalf("cardFromErr.Remedy = %q, want %q", cardFromErr.Remedy, RemedyEndpointRefused)
	}

	// 4. Verify Projector translates the event into an item with remedy intact
	items, err := NewProjector().Build([]agent.Event{ev})
	if err != nil || len(items) != 1 || items[0].Error == nil {
		t.Fatalf("projector failed to project error item: items=%#v err=%v", items, err)
	}
	if items[0].Error.Remedy != cardFromEvent.Remedy {
		t.Fatalf("projected ErrorCard.Remedy = %q, want %q", items[0].Error.Remedy, cardFromEvent.Remedy)
	}
}
