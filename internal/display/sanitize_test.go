package display_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"nabd/internal/display"
)

func TestSanitizeNeutralizesANSIAndControls(t *testing.T) {
	// CSI color and clear screen
	raw := "hello \x1b[31;1mworld\x1b[0m \x1b[2J"
	res := display.SanitizeForDisplay(raw, display.DisplayPolicy{AllowNewline: false})
	if strings.Contains(res, "\x1b") {
		t.Fatalf("ANSI escape survived: %q", res)
	}
	if res != "hello world " {
		t.Fatalf("unexpected sanitized text: %q", res)
	}
}

func TestSanitizeNeutralizesOSCHyperlinks(t *testing.T) {
	osc8 := "\x1b]8;;https://evil.example\x07Click Me\x1b]8;;\x07"
	res := display.SanitizeForDisplay(osc8, display.DisplayPolicy{AllowNewline: false})
	if strings.Contains(res, "\x1b") || strings.Contains(res, "evil.example") {
		t.Fatalf("OSC8 link leaked: %q", res)
	}
	if !strings.Contains(res, "Click Me") {
		t.Fatalf("link text missing: %q", res)
	}
}

func TestSanitizeNeutralizesNewlinesWhenNotAllowed(t *testing.T) {
	multiline := "line1\r\nline2\nline3\rline4"
	res := display.SanitizeForDisplay(multiline, display.DisplayPolicy{AllowNewline: false})
	if strings.Contains(res, "\n") || strings.Contains(res, "\r") {
		t.Fatalf("newline survived when AllowNewline=false: %q", res)
	}
	want := "line1 line2 line3line4"
	if res != want {
		t.Fatalf("got %q, want %q", res, want)
	}
}

func TestSanitizeRedactsCredentials(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		sentinel string
	}{
		{"Anthropic", "prefix sk-ant-api03-abcdef1234567890 suffix", "sk-ant-api03-abcdef1234567890"},
		{"OpenRouter", "prefix sk-or-v1-abcdef1234567890 suffix", "sk-or-v1-abcdef1234567890"},
		{"Groq", "prefix gsk_abcdef1234567890 suffix", "gsk_abcdef1234567890"},
		{"NVIDIA", "prefix nvapi-abcdef1234567890 suffix", "nvapi-abcdef1234567890"},
		{"GitHub", "prefix ghp_12345678901234567890 suffix", "ghp_12345678901234567890"},
		{"Bearer", "prefix Bearer secrettoken12345678 suffix", "secrettoken12345678"},
		{"AuthorizationHeader", "prefix Authorization: secrettoken12345678 suffix", "secrettoken12345678"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := display.SanitizeForDisplay(tc.input, display.DisplayPolicy{Redact: true})
			if strings.Contains(res, tc.sentinel) {
				t.Fatalf("sentinel value leaked for case %s", tc.name)
			}
			if !strings.Contains(res, display.RedactedToken) {
				t.Fatalf("redacted token missing for case %s: %q", tc.name, res)
			}
		})
	}
}

func TestSanitizeNeutralizesBidiOverrides(t *testing.T) {
	spoofed := "normal_name\u202Eexe.doc"
	res := display.SanitizeForDisplay(spoofed, display.DisplayPolicy{})
	if strings.Contains(res, "\u202E") {
		t.Fatalf("bidi override survived: %q", res)
	}
}

func TestSanitizePreservesLegitimateArabicAndEmoji(t *testing.T) {
	arabic := "تنبيه: فشل المسار"
	if res := display.SanitizeForDisplay(arabic, display.DisplayPolicy{}); res != arabic {
		t.Fatalf("Arabic text modified: got %q, want %q", res, arabic)
	}

	emoji := "👨‍👩‍👧‍👦"
	if res := display.SanitizeForDisplay(emoji, display.DisplayPolicy{}); res != emoji {
		t.Fatalf("Emoji modified: got %q, want %q", res, emoji)
	}
}

func TestSanitizeValidUTF8(t *testing.T) {
	invalid := string([]byte{'a', 0xff, 'b'})
	res := display.SanitizeForDisplay(invalid, display.DisplayPolicy{})
	if !utf8.ValidString(res) {
		t.Fatalf("output is not valid UTF-8: %q", res)
	}
	if !strings.Contains(res, "\uFFFD") {
		t.Fatalf("replacement char missing: %q", res)
	}
}
