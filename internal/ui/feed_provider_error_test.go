package ui

import (
	"fmt"
	"strings"
	"testing"

	"nabd/internal/endpoint"
	"nabd/internal/presentation"
)

// TestFeedProviderErrorShowsEndpointRefusedRemedy drives the interactive feed
// through Feed.Update — the same entry point the Bubble Tea event loop uses —
// with a provider error, and asserts the resulting card carries the
// endpoint_refused remedy.
//
// This is the coverage the review asked for and the one that was missing:
// TestCrossPackageEndpointRefusedRemedyFlow calls presentation.NewErrorCard
// directly and never enters Feed.Update, so it cannot see a local classifier
// inside the feed that would drop the code to "unknown" and lose the remedy.
func TestFeedProviderErrorShowsEndpointRefusedRemedy(t *testing.T) {
	f := NewFeed()
	f.width, f.height = 80, 24

	baseErr := fmt.Errorf("baseURL http://127.0.0.1:8118 uses scheme http: %w", endpoint.ErrEndpointRefused)
	_, _ = f.Update(modelsResultMsg{provider: "acme", err: baseErr})

	joined := strings.Join(f.lines, "\n")
	if !strings.Contains(joined, string(presentation.ErrCodeEndpointRefused)) {
		t.Fatalf("feed does not show endpoint_refused code:\n%s", joined)
	}
	if !strings.Contains(joined, "remedy:") {
		t.Fatalf("feed does not show a remedy row:\n%s", joined)
	}
	// The remedy is wrapped to the terminal width, so compare on a
	// whitespace-normalized form rather than the raw line.
	flat := strings.Join(strings.Fields(joined), " ")
	wantRemedy := strings.Join(strings.Fields(presentation.RemedyEndpointRefused), " ")
	if !strings.Contains(flat, wantRemedy) {
		t.Fatalf("feed does not show the endpoint_refused remedy %q:\n%s", presentation.RemedyEndpointRefused, joined)
	}
}
