package ui

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/providercmd"

	tea "github.com/charmbracelet/bubbletea"
)

// TestChatModelsRunsOffTheEventLoop is the regression guard for the frozen
// Chat: /models used to call OnModels synchronously inside Update with a
// context.Background() that nothing could cancel, so the Bubble Tea loop
// stopped drawing and Ctrl+C stopped working for the whole probe.
//
// The proof is structural, not timing-based: after Update returns, OnModels
// must NOT have run yet (it belongs to the returned tea.Cmd), and View() must
// still render the fetching state while the probe is blocked. On the old code
// OnModels fired inside Update, so the "not yet called" assertion fails.
func TestChatModelsRunsOffTheEventLoop(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})

	c := NewChat(runnerStub{}, make(chan agent.Event, 1))
	c.SetCallbacks(&SessionCallbacks{
		OnModels: func(ctx context.Context, providerID string) ([]string, string, error) {
			close(entered)
			<-release
			return []string{"model-1"}, "disclaimer", nil
		},
	})

	for _, r := range "/models mockserver" {
		c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	_, cmd := c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("/models must return an async tea.Cmd; a synchronous probe blocks the event loop")
	}

	// Update returned without running the probe.
	select {
	case <-entered:
		t.Fatal("OnModels ran inside Update: the event loop was blocked")
	default:
	}

	// The model is drawable while the network call is still pending.
	if view := c.View(); !strings.Contains(view, "fetching models") {
		t.Fatalf("View() must render the pending state during the probe, got %q", view)
	}

	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-entered // the probe is now in flight, still blocked

	if view := c.View(); view == "" {
		t.Fatal("View() rendered nothing while the probe was in flight")
	}
	// Ctrl+C must be able to cancel the probe: the fetch holds running/cancel,
	// which is what the safety key reads.
	if !c.running || c.cancel == nil {
		t.Fatal("the probe must hold running/cancel so Ctrl+C and the send gate cover it")
	}

	close(release)
	c.Update(<-done)

	if !strings.Contains(c.View(), "model-1") {
		t.Fatalf("settled models not rendered: %q", c.View())
	}
	if c.running || c.cancel != nil {
		t.Fatal("running/cancel must be released when the probe settles")
	}
}

// TestChatModelsCancelShowsCanceled proves the probe's context is cancellable
// and that the handler recognizes the error chain a real canceled request
// produces. A transport cancel is not a bare context.Canceled: it arrives as
// providercmd's classified error wrapping *url.Error wrapping context.Canceled.
// The handler must unwrap that (errors.Is), not compare text or identity, or a
// cancellation would be reported as a provider failure.
func TestChatModelsCancelShowsCanceled(t *testing.T) {
	// The real chain, built on the test goroutine so a setup failure is a test
	// failure rather than a t.Fatal from a command goroutine.
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, wrappedErr := providercmd.FetchModels(canceledCtx, "openai", "http://127.0.0.1:1", "k", &http.Client{})
	if wrappedErr == nil {
		t.Fatal("setup: a canceled context must produce a transport error")
	}
	if !errors.Is(wrappedErr, context.Canceled) {
		t.Fatalf("setup: the transport error must wrap context.Canceled, got %#v", wrappedErr)
	}

	cases := []struct {
		name string
		run  func(ctx context.Context) error
	}{
		{
			name: "bare context.Canceled",
			run:  func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
		},
		{
			name: "classified wraps url.Error wraps context.Canceled",
			run:  func(context.Context) error { return wrappedErr },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewChat(runnerStub{}, make(chan agent.Event, 1))
			c.SetCallbacks(&SessionCallbacks{
				OnModels: func(ctx context.Context, providerID string) ([]string, string, error) {
					return nil, "", tc.run(ctx)
				},
			})

			for _, r := range "/models mockserver" {
				c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
			_, cmd := c.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if cmd == nil {
				t.Fatal("expected an async cmd")
			}

			done := make(chan tea.Msg, 1)
			go func() { done <- cmd() }()

			c.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			c.Update(<-done)

			if got := c.Status(); got != "canceled" {
				t.Fatalf("status after cancel = %q, want canceled", got)
			}
		})
	}
}
