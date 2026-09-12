package redact

import (
	"strings"
	"testing"
)

func TestRedactRecognizedCredentials(t *testing.T) {
	tests := []struct {
		name   string
		secret string
	}{
		{"anthropic", "sk-ant-abcdefgh12345678"},
		{"openrouter", "sk-or-abcdefgh12345678"},
		{"groq", "gsk_abcdefgh12345678"},
		{"nvidia", "nvapi-abcdefgh12345678"},
		{"bearer", "Bearer abcdefgh12345678"},
		{"authorization", "Authorization: abcdefgh12345678"},
		{"github-fine-grained", "github_pat_abcdefghijklmnop12345678"},
		{"github-classic", "ghp_abcdefghijklmnop12345678"},
		{"gitlab", "glpat-abcdefghijklmnop12345678"},
		{"slack", "xoxb-abcdefghijklmnop12345678"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact("before " + tc.secret + " after")
			if strings.Contains(got, tc.secret) {
				t.Fatalf("credential remained after redaction: %q", got)
			}
			if !strings.Contains(got, Token) {
				t.Fatalf("replacement token missing: %q", got)
			}
		})
	}
}

func TestRedactExactKeys(t *testing.T) {
	const secret = "custom-secret-without-known-prefix"
	got := RedactExactKeys("before "+secret+" after", []string{"", secret})

	if strings.Contains(got, secret) {
		t.Fatalf("exact credential remained: %q", got)
	}
	if got != "before "+Token+" after" {
		t.Fatalf("unexpected exact-key redaction: %q", got)
	}
}

func TestRedactIsIdempotent(t *testing.T) {
	input := "Authorization: abcdefgh12345678"
	once := Redact(input)
	twice := Redact(once)

	if twice != once {
		t.Fatalf("redaction is not idempotent: once=%q twice=%q", once, twice)
	}
}

func TestRedactPreservesOrdinaryUnicode(t *testing.T) {
	input := "رسالة عربية مع تشكيل: اَلْعَرَبِيَّةُ و emoji 👨‍👩‍👧‍👦"
	if got := Redact(input); got != input {
		t.Fatalf("ordinary Unicode changed: got %q want %q", got, input)
	}
}

func TestSanitizeBodyRedactsBeforeTruncation(t *testing.T) {
	const secret = "custom-secret-without-known-prefix"
	input := secret + strings.Repeat("x", MaxBodyBytes*2)

	got := SanitizeBody(input, []string{secret})
	if strings.Contains(got, secret) {
		t.Fatalf("credential survived sanitization: %q", got)
	}
	if !strings.Contains(got, Token) {
		t.Fatalf("replacement token missing: %q", got)
	}
	if !strings.HasSuffix(got, "…[truncated]") {
		t.Fatalf("truncation marker missing")
	}
}
