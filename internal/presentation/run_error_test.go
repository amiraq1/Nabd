package presentation_test

import (
	"fmt"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/presentation"
)

// exhaustedErr is the real error body from the reported session: the
// headline says the run is over, the indented lines say why.
const exhaustedErr = "all 2 route(s) exhausted (mixed failures); shortest retry-after: 20s\n" +
	"  [groq:openai/gpt-oss-120b]: Rate limit reached for model in organization on tokens per minute (TPM): Limit 8000, Used 6605, Requested 3990\n" +
	"  [nvidia:moonshotai/kimi-k2.6]: prestream timeout"

const leakyErr = "auth failed\n  Authorization: Bearer sk-ant-api03-abcdef0123456789abcdef0123456789"

func TestFormatRunErrorKeepsCodeAndRouteDetail(t *testing.T) {
	v := presentation.FormatRunError(agent.Event{
		Type:      agent.RunError,
		Err:       exhaustedErr,
		ErrorCode: "budget",
	})

	if !strings.Contains(v.Headline, "exhausted") || !strings.Contains(v.Headline, "budget") {
		t.Errorf("headline = %q, want the message and the code", v.Headline)
	}
	if len(v.Details) != 2 {
		t.Fatalf("details = %v, want the two per-route causes", v.Details)
	}
	if !strings.Contains(v.Details[0], "groq") || !strings.Contains(v.Details[1], "prestream timeout") {
		t.Errorf("route causes lost: %v", v.Details)
	}
	if !strings.Contains(v.Hint, "NABD_ROUTER_RETRY_AFTER_WAIT") {
		t.Errorf("hint = %q, want an actionable next step", v.Hint)
	}
}

func TestFormatRunErrorHidesUnknownCode(t *testing.T) {
	cases := map[string]string{"empty": "", "explicit": "unknown"}
	for name, code := range cases {
		t.Run(name, func(t *testing.T) {
			v := presentation.FormatRunError(agent.Event{
				Type:      agent.RunError,
				Err:       "boom",
				ErrorCode: code,
			})
			if v.Headline != "boom" {
				t.Fatalf("headline = %q, want %q", v.Headline, "boom")
			}
			if v.Hint != "" {
				t.Fatalf("hint = %q, want none for an unknown code", v.Hint)
			}
		})
	}
}

func TestFormatRunErrorHintsPerCode(t *testing.T) {
	cases := map[string]string{
		"provider_auth": "~/.ag/config",
		"persist":       "~/.ag",
		"budget":        "NABD_ROUTER_RETRY_AFTER_WAIT",
	}
	for code, want := range cases {
		v := presentation.FormatRunError(agent.Event{
			Type:      agent.RunError,
			Err:       "x",
			ErrorCode: code,
		})
		if !strings.Contains(v.Hint, want) {
			t.Errorf("code %s: hint = %q, want it to mention %q", code, v.Hint, want)
		}
		if !strings.Contains(v.Headline, code) {
			t.Errorf("code %s missing from headline %q", code, v.Headline)
		}
	}
}

// TestFormatRunErrorNeverRendersEmpty keeps a failure visible even when the
// loop journaled no message: an empty row reads as "nothing happened".
func TestFormatRunErrorNeverRendersEmpty(t *testing.T) {
	v := presentation.FormatRunError(agent.Event{Type: agent.RunError})
	if v.Headline == "" || len(v.Lines()) == 0 {
		t.Fatalf("empty failure produced no visible line: %+v", v)
	}
}

func TestFormatRunErrorIncludesHTTPStatus(t *testing.T) {
	v := presentation.FormatRunError(agent.Event{
		Type:      agent.RunError,
		Err:       "provider refused",
		ErrorCode: "provider_auth",
		Code:      401,
	})
	if !strings.Contains(v.Headline, "401") {
		t.Fatalf("headline = %q, want the HTTP status", v.Headline)
	}
}

// TestFormatRunErrorRedactsSecrets holds the new surface inside the existing
// disclosure contract: an error body is provider-controlled text.
func TestFormatRunErrorRedactsSecrets(t *testing.T) {
	v := presentation.FormatRunError(agent.Event{
		Type:      agent.RunError,
		Err:       leakyErr,
		ErrorCode: "provider_auth",
	})
	for _, line := range v.Lines() {
		if strings.Contains(line, "sk-ant-api03-") {
			t.Fatalf("secret leaked: %q", line)
		}
		if strings.ContainsAny(line, "\n\r") {
			t.Fatalf("line is not a single line: %q", line)
		}
	}
}

// TestFormatRunErrorCapsDetails stops a long provider error from pushing the
// conversation off the screen.
func TestFormatRunErrorCapsDetails(t *testing.T) {
	var b strings.Builder
	b.WriteString("headline")
	for i := 0; i < 40; i++ {
		b.WriteString(fmt.Sprintf("\n  route %d failed", i))
	}
	v := presentation.FormatRunError(agent.Event{Type: agent.RunError, Err: b.String()})
	if len(v.Details) > 7 {
		t.Fatalf("details not capped: %d lines", len(v.Details))
	}
	if last := v.Details[len(v.Details)-1]; !strings.Contains(last, "more in the journal") {
		t.Fatalf("truncation not disclosed: %q", last)
	}
}
