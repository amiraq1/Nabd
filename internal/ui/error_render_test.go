package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"
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
