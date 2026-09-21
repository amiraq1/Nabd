package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/providercmd"
	"nabd/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

// TestConnectSecretNeverEntersTheJournal proves that a distinct credential
// enrolled via the interactive /connect slash command never enters the session
// journal, event stream, model prompt context, or screen rendering, but is saved
// to ~/.ag/auth.json at mode 0600.
func TestConnectSecretNeverEntersTheJournal(t *testing.T) {
	tmpDir := t.TempDir()
	authPath := filepath.Join(tmpDir, "auth.json")
	providersPath := filepath.Join(tmpDir, "providers.json")
	t.Setenv("NABD_AUTH_FILE", authPath)
	t.Setenv("NABD_PROVIDERS_FILE", providersPath)

	// Distinct canary secret string with unmistakable bytes.
	const canarySecret = "sk-antigravity-canary-secret-never-leak-9988776655"

	sess, err := newInteractiveSession(nil)
	if err != nil {
		t.Fatalf("newInteractiveSession: %v", err)
	}
	sink := &recordSink{}
	sess.loop.Sink = sink
	cb := sess.callbacks()

	// -------------------------------------------------------------------------
	// 1. Feed frontend path: hidden input prompt
	// -------------------------------------------------------------------------
	feed := ui.NewFeed()
	feed.SetCallbacks(cb)

	// Dispatch /connect testprov
	for _, r := range "/connect testprov" {
		feed.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	feed.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Enter canary secret keystroke by keystroke while in secretPrompt mode
	for _, r := range canarySecret {
		feed.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	// Secret bytes must never appear in composer or view output during entry
	if strings.Contains(feed.View(), canarySecret) {
		t.Fatalf("Feed.View() leaked secret during typing: %q", feed.View())
	}
	if strings.Contains(feed.ComposerValue(), canarySecret) {
		t.Fatalf("Feed.composer leaked secret during typing: %q", feed.ComposerValue())
	}

	// Press Enter to complete /connect
	feed.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Verify auth.json was created with mode 0600 and contains the canary secret
	fi, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("auth.json not written: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("auth.json permission = %04o, want 0600", fi.Mode().Perm())
	}
	authBytes, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(authBytes), canarySecret) {
		t.Fatalf("auth.json missing canary secret: %s", string(authBytes))
	}

	// Verify secret does not appear in Feed view or composer after submission
	if strings.Contains(feed.View(), canarySecret) {
		t.Fatalf("Feed.View() leaked secret after submission: %q", feed.View())
	}
	if strings.Contains(feed.ComposerValue(), canarySecret) {
		t.Fatalf("Feed.composer leaked secret after submission: %q", feed.ComposerValue())
	}

	// -------------------------------------------------------------------------
	// 2. Refusal of key as command-line argument: /connect testprov <secret>
	// -------------------------------------------------------------------------
	for _, r := range "/connect testprov " + canarySecret {
		feed.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	feed.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if feed.Status() != providercmd.ErrKeyAsArgument.Error() {
		t.Fatalf("Feed status = %q, want ErrKeyAsArgument %q", feed.Status(), providercmd.ErrKeyAsArgument.Error())
	}
	if strings.Contains(feed.ComposerValue(), canarySecret) {
		t.Fatalf("Feed composer retained secret argument on refusal: %q", feed.ComposerValue())
	}
	if strings.Contains(feed.View(), canarySecret) {
		t.Fatalf("Feed.View() leaked secret argument on refusal: %q", feed.View())
	}

	// -------------------------------------------------------------------------
	// 3. Chat frontend path
	// -------------------------------------------------------------------------
	chatEvents := make(chan agent.Event, 10)
	chat := ui.NewChat(sess.loop, chatEvents)
	chat.SetCallbacks(cb)

	for _, r := range "/connect testprov" {
		chat.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	chat.Update(tea.KeyMsg{Type: tea.KeyEnter})

	for _, r := range canarySecret {
		chat.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if strings.Contains(chat.View(), canarySecret) {
		t.Fatalf("Chat.View() leaked secret during typing: %q", chat.View())
	}
	chat.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if strings.Contains(chat.View(), canarySecret) {
		t.Fatalf("Chat.View() leaked secret after submission: %q", chat.View())
	}

	// Chat rejection of a key passed as an argument, driven by keypresses so
	// the test exercises the KeyEnter handler that clears m.input. Calling
	// Command() directly skipped that path, so the canary never entered the
	// model and the old no-leak assertion was vacuous.
	for _, r := range "/connect testprov " + canarySecret {
		chat.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	chat.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if chat.Status() != providercmd.ErrKeyAsArgument.Error() {
		t.Fatalf("Chat status = %q, want ErrKeyAsArgument", chat.Status())
	}
	if strings.Contains(chat.View(), canarySecret) {
		t.Fatalf("Chat.View() leaked the refused secret argument: %q", chat.View())
	}

	// -------------------------------------------------------------------------
	// 4. Invariant: ZERO secret bytes ever reached events, journal, or model history
	// -------------------------------------------------------------------------
	for _, ev := range sink.evs {
		evJSON, _ := json.Marshal(ev)
		if strings.Contains(string(evJSON), canarySecret) {
			t.Fatalf("secret leaked into journal event: %s", string(evJSON))
		}
		if strings.Contains(ev.Text, canarySecret) || strings.Contains(ev.Err, canarySecret) {
			t.Fatalf("secret leaked into event text/err: %+v", ev)
		}
	}

	for _, ev := range agent.Live(sess.loop.Hist()) {
		evJSON, _ := json.Marshal(ev)
		if strings.Contains(string(evJSON), canarySecret) {
			t.Fatalf("secret leaked into live session history event: %s", string(evJSON))
		}
	}

	for _, m := range agent.Messages(agent.Live(sess.loop.Hist())) {
		if strings.Contains(m.Text, canarySecret) {
			t.Fatalf("secret leaked into model message text: %q", m.Text)
		}
		for _, tc := range m.ToolCalls {
			if strings.Contains(string(tc.Input), canarySecret) {
				t.Fatalf("secret leaked into model tool call input: %q", string(tc.Input))
			}
		}
		for _, tr := range m.ToolResults {
			if strings.Contains(tr.Output, canarySecret) {
				t.Fatalf("secret leaked into model tool result output: %q", tr.Output)
			}
		}
	}
}

// TestSessionProviderAndModelsCommands verifies the behavior of /provider and /models
// in both Feed and Chat, verifying live model fetch, disclaimer note, and error card rendering.
func TestSessionProviderAndModelsCommands(t *testing.T) {
	// Mock endpoint for /models
	var failWithAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		if failWithAuth {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintln(w, `{"error":"invalid_api_key"}`)
			return
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprintln(w, `{"data":[{"id":"mock-gpt-4o"},{"id":"mock-claude-3"}]}`)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	authPath := filepath.Join(tmpDir, "auth.json")
	providersPath := filepath.Join(tmpDir, "providers.json")
	t.Setenv("NABD_AUTH_FILE", authPath)
	t.Setenv("NABD_PROVIDERS_FILE", providersPath)
	t.Setenv("NABD_ENDPOINT_POLICY", "open") // httptest uses http://127.0.0.1; disable the policy gate for this unit test

	provJSON := fmt.Sprintf(`{
		"provider": {
			"mockserver": {
				"api": "openai",
				"options": {
					"baseURL": "%s"
				},
				"models": {
					"mock-default": {}
				}
			}
		}
	}`, server.URL)
	if err := os.WriteFile(providersPath, []byte(provJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	authJSON := `{
		"mockserver": {
			"type": "api",
			"key": "mock-test-key"
		}
	}`
	if err := os.WriteFile(authPath, []byte(authJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	sess, err := newInteractiveSession(nil)
	if err != nil {
		t.Fatalf("newInteractiveSession: %v", err)
	}
	cb := sess.callbacks()

	// 1. Test /provider in Chat and Feed
	chat := ui.NewChat(sess.loop, make(chan agent.Event, 1))
	chat.SetCallbacks(cb)
	chatProvRes := chat.Command("/provider")
	if !strings.Contains(chatProvRes, "mockserver") || !strings.Contains(chatProvRes, "openai") {
		t.Fatalf("Chat /provider output = %q, want mockserver listing", chatProvRes)
	}

	feed := ui.NewFeed()
	feed.SetCallbacks(cb)
	feed.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for _, r := range "/provider" {
		feed.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	feed.Update(tea.KeyMsg{Type: tea.KeyEnter}) // completes menu if open
	feed.Update(tea.KeyMsg{Type: tea.KeyEnter}) // executes command
	if !strings.Contains(feed.View(), "mockserver") {
		t.Fatalf("Feed /provider view missing mockserver:\n%s", feed.View())
	}

	// 2. Test /models in Chat
	chatModelsRes := chat.Command("/models mockserver")
	if !strings.Contains(chatModelsRes, "mock-gpt-4o") || !strings.Contains(chatModelsRes, "mock-claude-3") {
		t.Fatalf("Chat /models missing models: %s", chatModelsRes)
	}
	if !strings.Contains(chatModelsRes, providercmd.CatalogIsNotACredentialCheck) {
		t.Fatalf("Chat /models missing credential disclaimer note: %s", chatModelsRes)
	}

	// 3. Test /models in Feed (success path)
	for _, r := range "/models mockserver" {
		feed.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	_, cmd := feed.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Feed /models must return async tea.Cmd")
	}
	msg := cmd()
	feed.Update(msg)

	feedView := feed.View()
	if !strings.Contains(feedView, "mock-gpt-4o") || !strings.Contains(feedView, "mock-claude-3") {
		t.Fatalf("Feed view missing fetched models:\n%s", feedView)
	}
	if !strings.Contains(feedView, "does not prove the API key is valid") {
		t.Fatalf("Feed view missing credential disclaimer note:\n%s", feedView)
	}

	// 4. Test /models in Feed (error path: 401 unauthorized produces 4-field ErrorCard)
	failWithAuth = true
	for _, r := range "/models mockserver" {
		feed.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	_, cmdErr := feed.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmdErr == nil {
		t.Fatal("Feed /models must return async tea.Cmd on error")
	}
	errMsg := cmdErr()
	feed.Update(errMsg)

	errView := feed.View()
	// Check ErrorCard 4 fields: code, details, action (and title/mark)
	if !strings.Contains(errView, "provider_auth") {
		t.Fatalf("error view missing code 'provider_auth':\n%s", errView)
	}
	if !strings.Contains(errView, "action:") {
		t.Fatalf("error view missing 'action:':\n%s", errView)
	}
	if !strings.Contains(errView, "details:") {
		t.Fatalf("error view missing 'details:':\n%s", errView)
	}
}
