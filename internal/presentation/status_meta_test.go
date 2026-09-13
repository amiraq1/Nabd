package presentation_test

import (
	"strings"
	"testing"
	"time"

	"nabd/internal/agent"
	"nabd/internal/presentation"
)

func TestMetaTracksTurnTokensAndCommittedRoute(t *testing.T) {
	start := time.Date(2026, 9, 13, 5, 0, 0, 0, time.UTC)
	p := presentation.NewStatusProjector()
	for _, e := range []agent.Event{
		{Seq: 1, Type: agent.RunStart, Time: start},
		{Seq: 2, Type: agent.TurnStart, Time: start.Add(time.Second)},
		{Seq: 3, Type: agent.EventProviderRoute, Route: &agent.ProviderRoute{
			Status: "failed", Provider: "groq", Model: "openai/gpt-oss-120b", Attempt: 1,
			Reason: "429 rate limit",
		}},
		{Seq: 4, Type: agent.EventProviderRoute, Route: &agent.ProviderRoute{
			Status: "selected", Provider: "nvidia", Model: "moonshotai/kimi-k2.6", Attempt: 2,
			StreamID: "stream-secret-xyz",
		}},
		{Seq: 5, Type: agent.EventProviderUsage, Usage: &agent.ProviderUsage{
			PromptTokens: 6000, CompletionTokens: 605,
		}},
	} {
		p.Apply(e)
	}

	m := p.Meta(start.Add(12 * time.Second))
	if m.Turn != 1 {
		t.Errorf("turn = %d, want 1", m.Turn)
	}
	if m.Tokens != 6605 {
		t.Errorf("tokens = %d, want 6605", m.Tokens)
	}
	if m.Elapsed != 12*time.Second {
		t.Errorf("elapsed = %v, want 12s", m.Elapsed)
	}
	// The failed groq attempt must not be reported as the serving provider.
	if m.Provider != "nvidia" || m.Model != "moonshotai/kimi-k2.6" {
		t.Errorf("route = %s/%s, want nvidia/moonshotai/kimi-k2.6", m.Provider, m.Model)
	}

	widest := presentation.RuntimeMetaVariants(m)[0]
	if widest != "turn 1 · 6.6k tok · 12s · nvidia/moonshotai/kimi-k2.6" {
		t.Fatalf("widest variant = %q", widest)
	}
	if strings.Contains(widest, "stream-secret-xyz") {
		t.Fatalf("stream ID leaked: %q", widest)
	}
}

// TestMetaElapsedFreezesAtTerminalEvent is the counterpart of the status-row
// truthfulness rule: a dead run must not appear to still be running.
func TestMetaElapsedFreezesAtTerminalEvent(t *testing.T) {
	start := time.Date(2026, 9, 13, 5, 0, 0, 0, time.UTC)
	for name, terminal := range map[string]agent.Event{
		"run_error":   {Seq: 2, Type: agent.RunError, Time: start.Add(5 * time.Second), Err: "boom"},
		"run_end":     {Seq: 2, Type: agent.RunEnd, Time: start.Add(5 * time.Second)},
		"interrupted": {Seq: 2, Type: agent.Interrupted, Time: start.Add(5 * time.Second)},
	} {
		t.Run(name, func(t *testing.T) {
			p := presentation.NewStatusProjector()
			p.Apply(agent.Event{Seq: 1, Type: agent.RunStart, Time: start})
			p.Apply(terminal)
			if got := p.Meta(start.Add(time.Hour)).Elapsed; got != 5*time.Second {
				t.Fatalf("elapsed = %v, want 5s", got)
			}
		})
	}
}

func TestRuntimeMetaVariantsNarrowMonotonically(t *testing.T) {
	variants := presentation.RuntimeMetaVariants(presentation.RuntimeMeta{
		Provider: "groq", Model: "openai/gpt-oss-120b",
		Turn: 3, Tokens: 6605, Elapsed: 95 * time.Second,
	})
	if len(variants) < 3 {
		t.Fatalf("expected several variants, got %v", variants)
	}
	if variants[len(variants)-1] != "turn 3" {
		t.Errorf("narrowest variant = %q, want \"turn 3\"", variants[len(variants)-1])
	}
	if !strings.Contains(variants[0], "1m35s") {
		t.Errorf("expected m:ss elapsed in %q", variants[0])
	}
	for i := 1; i < len(variants); i++ {
		if len(variants[i]) > len(variants[i-1]) {
			t.Fatalf("variant %d (%q) is wider than %d (%q)", i, variants[i], i-1, variants[i-1])
		}
	}
}

func TestRuntimeMetaVariantsEmptyWhenNothingKnown(t *testing.T) {
	if got := presentation.RuntimeMetaVariants(presentation.RuntimeMeta{}); len(got) != 0 {
		t.Fatalf("expected no variants for empty meta, got %v", got)
	}
}

// TestRuntimeMetaSanitizesRouteFields keeps provider-supplied strings inside
// the same disclosure contract as route notices.
func TestRuntimeMetaSanitizesRouteFields(t *testing.T) {
	variants := presentation.RuntimeMetaVariants(presentation.RuntimeMeta{
		Turn:     1,
		Provider: "groq\nBearer sk-ant-api03-abcdef0123456789abcdef0123456789",
	})
	if len(variants) == 0 {
		t.Fatal("expected at least one variant")
	}
	for _, v := range variants {
		if strings.Contains(v, "sk-ant-api03-") {
			t.Fatalf("secret leaked: %q", v)
		}
		if strings.ContainsAny(v, "\n\r") {
			t.Fatalf("variant is not a single line: %q", v)
		}
	}
}

func TestTokenFormatting(t *testing.T) {
	cases := map[int]string{
		1:     "1 tok",
		999:   "999 tok",
		1000:  "1k tok",
		6605:  "6.6k tok",
		12000: "12k tok",
	}
	for tokens, want := range cases {
		variants := presentation.RuntimeMetaVariants(presentation.RuntimeMeta{Turn: 1, Tokens: tokens})
		if !strings.Contains(variants[0], want) {
			t.Errorf("tokens %d formatted as %q, want %q", tokens, variants[0], want)
		}
	}
}
