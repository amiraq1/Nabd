package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// enterSecretPrompt drives the Feed into /connect key entry via the same keys
// a user presses, so the test never bypasses routeKey. /connect only opens the
// prompt when the hook is wired, so the callback is installed here.
func enterSecretPrompt(t *testing.T, f *Feed) {
	t.Helper()
	f.SetCallbacks(&SessionCallbacks{
		OnConnect: func(providerID, key string) (string, error) { return "connected", nil },
	})
	for _, r := range "/connect testprov" {
		f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !f.secretPrompt {
		t.Fatal("setup: /connect did not enter the secret prompt")
	}
}

// TestFeedSecretPromptSurvivesStatusRanking pins that the secret-mode
// indicator is a property of the view, not of the transient status row: even
// after a higher-ranked status lands and is then cleared, the composer slot
// must still show that hidden input is active. The indicator must never be
// something a status update can erase.
func TestFeedSecretPromptSurvivesStatusRanking(t *testing.T) {
	f := NewFeed()
	f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	enterSecretPrompt(t, f)

	if v := f.View(); !strings.Contains(v, "API key (input hidden)") {
		t.Fatalf("secret mode not indicated after entry:\n%s", v)
	}

	f.setStatus("run ended with an error", rankRunLifecycle)
	if v := f.View(); !strings.Contains(v, "API key (input hidden)") {
		t.Fatalf("a higher-ranked status replaced the secret indicator:\n%s", v)
	}

	f.clearStatus()
	if v := f.View(); !strings.Contains(v, "API key (input hidden)") {
		t.Fatalf("clearing the status dropped the secret indicator:\n%s", v)
	}
}

// TestFeedSecretPromptIgnoresPointerGestures pins that a pointer gesture
// cannot act while a credential is being typed: no press is tracked, no
// navigation mode is entered, no card is selected, and the prompt stays
// active. Mouse is explicitly enabled so the secret guard — not the disabled
// mouse path — is what the assertions measure.
func TestFeedSecretPromptIgnoresPointerGestures(t *testing.T) {
	t.Setenv("NABD_NO_MOUSE", "")
	f := NewFeed()
	f.SetTouch(true)
	f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !f.MouseEnabled() {
		t.Fatal("setup: mouse must be enabled so the secret guard is what blocks the gesture")
	}
	enterSecretPrompt(t, f)

	f.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 5, Y: 1})
	if f.pointerDown {
		t.Fatal("a pointer press must not be tracked while the secret prompt is active")
	}

	f.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, X: 5, Y: 1})
	if f.navigationMode {
		t.Fatal("a pointer release must not enter navigation mode while the secret prompt is active")
	}
	if f.selectedItem >= 0 {
		t.Fatalf("a pointer gesture must not select a card while the secret prompt is active (selected=%d)", f.selectedItem)
	}
	if !f.secretPrompt {
		t.Fatal("the secret prompt must remain active after a pointer gesture")
	}
}
