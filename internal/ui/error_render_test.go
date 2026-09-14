package ui

import (
	"strings"
	"testing"
	"time"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

const renderExhaustedErr = "all 2 route(s) exhausted (mixed failures); shortest retry-after: 20s\n" +
	"  [groq:openai/gpt-oss-120b]: rate limit reached\n" +
	"  [nvidia:moonshotai/kimi-k2.6]: prestream timeout"

const renderLeakyErr = "auth failed: Authorization: Bearer sk-ant-api03-abcdef0123456789abcdef0123456789"

// TestRenderRunErrorShowsCodeAndHint is the E-series fix: the failure row
// used to print the provider message alone, dropping the journaled code and
// the remedy.
func TestRenderRunErrorShowsCodeAndHint(t *testing.T) {
	out := RenderEvent(agent.Event{
		Type:      agent.RunError,
		Err:       "provider rejected the request",
		ErrorCode: "provider_auth",
	}, 66)

	if !strings.Contains(out, "✗") {
		t.Errorf("failure marker missing: %q", out)
	}
	for _, want := range []string{"provider rejected the request", "provider_auth", "~/.ag/config"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestRenderRunErrorKeepsRouteCauses(t *testing.T) {
	out := RenderEvent(agent.Event{
		Type:      agent.RunError,
		Err:       renderExhaustedErr,
		ErrorCode: "budget",
	}, 80)

	for _, want := range []string{"groq", "prestream timeout"} {
		if !strings.Contains(out, want) {
			t.Errorf("route cause %q dropped from %q", want, out)
		}
	}
}

// TestRenderRunErrorRespectsWidth keeps the richer failure block inside the
// terminal: the phone terminal in the report is 66 columns.
func TestRenderRunErrorRespectsWidth(t *testing.T) {
	for _, width := range []int{20, 40, 66, 80} {
		out := RenderEvent(agent.Event{
			Type:      agent.RunError,
			Err:       renderExhaustedErr,
			ErrorCode: "budget",
		}, width)
		for _, line := range strings.Split(out, "\n") {
			if got := lineWidth(line); got > width {
				t.Fatalf("width %d: line %q is %d cells wide", width, line, got)
			}
		}
	}
}

func TestRenderRunErrorRedactsSecrets(t *testing.T) {
	out := RenderEvent(agent.Event{
		Type:      agent.RunError,
		Err:       renderLeakyErr,
		ErrorCode: "provider_auth",
	}, 66)
	if strings.Contains(out, "sk-ant-api03-") {
		t.Fatalf("secret leaked into the failure row: %q", out)
	}
}

// TestRenderRunErrorShowsWaitAtNarrowWidth is the regression for the route
// exhaustion error: the actionable number (how long to wait) must survive even
// at phone widths, where the older path hid it inside the free-text message that
// gets dropped from Details. RenderEvent routes RunError through
// presentation.FormatRunError, which now carries the retry-after as a structured
// field rendered on its own line regardless of width.
func TestRenderRunErrorShowsWaitAtNarrowWidth(t *testing.T) {
	ree := &provider.RouterExhaustedError{
		Attempts:   []provider.ProviderError{{Provider: "groq", Model: "m", Body: "x"}},
		RetryAfter: 20 * time.Second,
	}
	ev := agent.RunErrorEvent(ree)
	for _, width := range []int{24, 40, 66, 120} {
		out := RenderEvent(ev, width)
		// The wait line renders on its own line; at narrow widths it may wrap,
		// so check the two marker words independently rather than the joined
		// string.
		if !strings.Contains(out, "wait") {
			t.Errorf("width %d: 'wait' line missing from output: %q", width, out)
		}
		if !strings.Contains(out, "retry-after") {
			t.Errorf("width %d: 'retry-after' number missing from output: %q", width, out)
		}
		for _, line := range strings.Split(out, "\n") {
			if got := lineWidth(line); got > width {
				t.Fatalf("width %d: line %q is %d cells wide", width, line, got)
			}
		}
	}
}
