package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"nabd/internal/provider"

	tea "github.com/charmbracelet/bubbletea"
)

func TestFeedModels(t *testing.T) {
	t.Run("long list renders first and last model", func(t *testing.T) {
		models := make([]string, 33)
		for i := 0; i < 33; i++ {
			models[i] = fmt.Sprintf("model-%02d", i+1)
		}

		f := NewFeed()
		f.SetCallbacks(&SessionCallbacks{
			OnModels: func(ctx context.Context, providerID string) ([]string, string, error) {
				return models, "disclaimer note", nil
			},
		})
		f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

		for _, r := range "/models codecraft" {
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("expected async cmd")
		}
		msg := cmd()
		f.Update(msg)

		view := f.View()
		if !strings.Contains(view, "model-01") {
			t.Errorf("View() missing first model (model-01):\n%s", view)
		}
		if !strings.Contains(view, "model-33") {
			t.Errorf("View() missing last model (model-33):\n%s", view)
		}
	})

	t.Run("cancel with ctrl+c sets canceled status", func(t *testing.T) {
		entered := make(chan struct{})
		release := make(chan struct{})

		f := NewFeed()
		f.SetCallbacks(&SessionCallbacks{
			OnModels: func(ctx context.Context, providerID string) ([]string, string, error) {
				close(entered)
				<-ctx.Done()
				return nil, "", ctx.Err()
			},
		})
		f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

		for _, r := range "/models codecraft" {
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("expected async cmd")
		}

		done := make(chan tea.Msg, 1)
		go func() { done <- cmd() }()
		<-entered

		f.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		close(release)
		f.Update(<-done)

		if got := f.Status(); got != "canceled" {
			t.Fatalf("status after cancel = %q, want canceled", got)
		}
	})

	t.Run("error renders error notice", func(t *testing.T) {
		f := NewFeed()
		f.SetCallbacks(&SessionCallbacks{
			OnModels: func(ctx context.Context, providerID string) ([]string, string, error) {
				return nil, "", testProviderError{kind: provider.ErrorKindAuth, msg: "invalid api key"}
			},
		})
		f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

		for _, r := range "/models codecraft" {
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("expected async cmd")
		}
		msg := cmd()
		f.Update(msg)

		view := f.View()
		if !strings.Contains(view, "provider_auth") && !strings.Contains(view, "invalid api key") {
			t.Fatalf("View() missing error details:\n%s", view)
		}
	})
}

type testProviderError struct {
	kind provider.ErrorKind
	msg  string
}

func (e testProviderError) Error() string                 { return e.msg }
func (e testProviderError) ErrorKind() provider.ErrorKind { return e.kind }

func TestPTYFeedModels(t *testing.T) {
	models := make([]string, 33)
	for i := 0; i < 33; i++ {
		models[i] = fmt.Sprintf("model-%02d", i+1)
	}

	sess := StartPTYSession(t, 80, 24)
	defer sess.Close()

	sess.Feed.SetCallbacks(&SessionCallbacks{
		OnModels: func(ctx context.Context, providerID string) ([]string, string, error) {
			return models, "disclaimer note", nil
		},
	})

	sess.WriteString("/models codecraft\r")
	if err := sess.WaitForText("model-01", 3*time.Second); err != nil {
		t.Fatalf("first model did not appear: %v", err)
	}
	if err := sess.WaitForText("model-33", 3*time.Second); err != nil {
		t.Fatalf("last model did not appear: %v", err)
	}
}
