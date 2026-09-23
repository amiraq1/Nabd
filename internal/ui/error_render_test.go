package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"nabd/internal/agent"
	"nabd/internal/presentation"
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

// exhaustedEvent is the route-exhaustion failure as the loop journals it: the
// wait is inside the free-text message and, thanks to RunErrorEvent, also on the
// event as RetryAfter.
func exhaustedEvent() agent.Event {
	return agent.RunErrorEvent(&provider.RouterExhaustedError{
		Attempts: []provider.ProviderError{
			{Provider: "groq", Model: "m", Body: "rate limit reached"},
			{Provider: "nvidia", Model: "k", Body: "prestream timeout"},
		},
		RetryAfter: 20 * time.Second,
	})
}

// TestFeedErrorCardShowsWaitAtEveryWidth is the regression for the failure the
// report described: the feed card hides its details line below 40 columns and
// truncates it to the terminal width above that, so the wait the router reported
// — the one number the reader acts on — was unreadable on a phone terminal at
// every width except a wide one. The card now states it on its own line, and the
// width ladder may drop prose but never this.
func TestFeedErrorCardShowsWaitAtEveryWidth(t *testing.T) {
	items, err := presentation.NewProjector().Build([]agent.Event{exhaustedEvent()})
	if err != nil || len(items) != 1 {
		t.Fatalf("projection failed: items=%#v err=%v", items, err)
	}
	for _, width := range []int{20, 24, 39, 40, 66, 80, 120} {
		lines := renderError(items[0], width)
		out := strings.Join(lines, "\n")
		if !strings.Contains(out, "wait: 20s") {
			t.Errorf("width %d: wait line missing from the feed card: %q", width, out)
		}
		for _, line := range lines {
			if got := lineWidth(line); got > width {
				t.Fatalf("width %d: line %q is %d cells wide", width, line, got)
			}
		}
	}
}

// TestFeedErrorCardStatesNoWaitWhenNoneReported keeps 0 meaning "the router
// reported none" rather than "wait zero seconds".
func TestFeedErrorCardStatesNoWaitWhenNoneReported(t *testing.T) {
	ev := agent.RunErrorEvent(errors.New("auth failed: check the key"))
	items, err := presentation.NewProjector().Build([]agent.Event{ev})
	if err != nil || len(items) != 1 {
		t.Fatalf("projection failed: items=%#v err=%v", items, err)
	}
	if out := strings.Join(renderError(items[0], 66), "\n"); strings.Contains(out, "wait:") {
		t.Fatalf("card stated a wait that the router never reported: %q", out)
	}
}

// TestRenderRunErrorShowsWaitAtNarrowWidth covers the chat/scrollback surface:
// RunError goes through presentation.FormatRunError, which states the wait on
// its own line so it is not buried mid-sentence in the failure text.
func TestRenderRunErrorShowsWaitAtNarrowWidth(t *testing.T) {
	ev := exhaustedEvent()
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

func TestErrorCardRemedyShowsAtAllWidths(t *testing.T) {
	wantRemedy := presentation.RemedyEndpointRefused
	card := presentation.NewErrorCard(agent.ErrCodeEndpointRefused, "proxy endpoint refused", "")
	if card.Remedy != wantRemedy {
		t.Fatalf("card.Remedy = %q, want %q", card.Remedy, wantRemedy)
	}

	widths := []int{20, 40, 66}
	for _, width := range widths {
		lines := renderErrorCard(card, width)
		out := strings.Join(lines, "\n")
		// Check that the remedy label is present
		if !strings.Contains(out, "remedy:") {
			t.Errorf("width %d: 'remedy:' label missing from output:\n%s", width, out)
		}
		// Check that all words of the remedy are present in the output
		compactOut := strings.ReplaceAll(strings.ReplaceAll(out, "\n", ""), " ", "")
		compactWant := strings.ReplaceAll(strings.ReplaceAll(wantRemedy, "\n", ""), " ", "")
		if !strings.Contains(compactOut, compactWant) {
			t.Errorf("width %d: remedy content missing or truncated in card output:\n%s", width, out)
		}
		if width >= 40 {
			for _, word := range strings.Fields(wantRemedy) {
				if !strings.Contains(out, word) {
					t.Errorf("width %d: missing word %q from remedy in card output:\n%s", width, word, out)
				}
			}
		}
		// Check that no line exceeds the target width
		for _, l := range lines {
			if got := lineWidth(l); got > width {
				t.Fatalf("width %d: line %q is %d cells wide", width, l, got)
			}
		}
	}
}

func TestErrorCardRemedyRespectsNoColorAndAsciiOnly(t *testing.T) {
	card := presentation.NewErrorCard(agent.ErrCodeEndpointRefused, "proxy endpoint refused", "")

	t.Run("ColorProfile_Suppression", func(t *testing.T) {
		orig := lipgloss.ColorProfile()
		t.Cleanup(func() { lipgloss.SetColorProfile(orig) })

		// When TrueColor profile is active, lipgloss styles emit ANSI escapes
		lipgloss.SetColorProfile(termenv.TrueColor)
		linesColored := renderErrorCard(card, 50)
		hasANSI := false
		for _, l := range linesColored {
			if strings.Contains(l, "\x1b") {
				hasANSI = true
				break
			}
		}
		if !hasANSI {
			t.Fatalf("expected TrueColor profile to emit ANSI escapes in rendered error card")
		}

		// When Ascii profile is active (such as when NO_COLOR is configured), ANSI escapes are suppressed
		lipgloss.SetColorProfile(termenv.Ascii)
		for _, width := range []int{20, 40, 66} {
			lines := renderErrorCard(card, width)
			for _, l := range lines {
				if strings.Contains(l, "\x1b") {
					t.Fatalf("width %d: line contains ANSI escape with Ascii profile: %q", width, l)
				}
			}
		}
	})

	t.Run("NABD_ASCII_ONLY_DecorativeGlyphs", func(t *testing.T) {
		// Without NABD_ASCII_ONLY: error marker is ✗ and truncation tail is …
		t.Setenv("NABD_ASCII_ONLY", "")
		linesDefault := renderErrorCard(card, 66)
		joinedDefault := strings.Join(linesDefault, "\n")
		if !strings.Contains(joinedDefault, "✗ ") {
			t.Errorf("default error card missing decorative marker '✗ ': %s", joinedDefault)
		}

		// With NABD_ASCII_ONLY: marker becomes x and tail becomes ...
		t.Setenv("NABD_ASCII_ONLY", "1")
		linesASCII := renderErrorCard(card, 66)
		joinedASCII := strings.Join(linesASCII, "\n")
		if strings.Contains(joinedASCII, "✗ ") {
			t.Errorf("NABD_ASCII_ONLY error card still contains decorative marker '✗ ': %s", joinedASCII)
		}
		if !strings.Contains(joinedASCII, "x ") {
			t.Errorf("NABD_ASCII_ONLY error card missing ASCII replacement marker 'x ': %s", joinedASCII)
		}

		// Verify that Arabic text in the error message is preserved (non-ASCII text not mangled)
		cardArabic := presentation.NewErrorCard(agent.ErrCodeEndpointRefused, "فشل الاتصال بالخادم", "")
		linesArabic := renderErrorCard(cardArabic, 66)
		joinedArabic := strings.Join(linesArabic, "\n")
		if !strings.Contains(joinedArabic, "فشل الاتصال بالخادم") {
			t.Errorf("NABD_ASCII_ONLY mangled Arabic error text: %s", joinedArabic)
		}
		if strings.Contains(joinedArabic, "✗") || strings.Contains(joinedArabic, "…") {
			t.Errorf("NABD_ASCII_ONLY error card with Arabic text contains decorative non-ASCII glyphs: %s", joinedArabic)
		}
	})
}
