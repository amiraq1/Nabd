package redact

import (
	"regexp"
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

// extendedRedactCase covers the formats added for OpenAI, AWS, PEM blocks and
// bare JWTs, plus inputs that must survive untouched. A non-empty secret is the
// substring that must disappear; an empty secret means the whole input is
// benign and must be returned byte-for-byte unchanged.
type extendedRedactCase struct {
	name   string
	input  string
	secret string
}

var extendedRedactCases = []extendedRedactCase{
	{"openai-project-key", "before sk-proj-abcdefghijklmnopqrstuvwxyz0123456789 after", "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789"},
	{"openai-legacy-key", "before sk-abcdefghijklmnopqrstuvwxyz0123456789 after", "sk-abcdefghijklmnopqrstuvwxyz0123456789"},
	{"aws-access-key-id", "before AKIAIOSFODNN7EXAMPLE after", "AKIAIOSFODNN7EXAMPLE"},
	{"aws-session-key-id", "before ASIAIOSFODNN7EXAMPLE after", "ASIAIOSFODNN7EXAMPLE"},
	{"pem-private-key", "before -----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY----- after", "-----BEGIN RSA PRIVATE KEY-----"},
	{"pem-unterminated", "before -----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjE after", "-----BEGIN OPENSSH PRIVATE KEY-----"},
	{"bare-jwt", "before eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c after", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"},
	{"word-sk-learn", "sk-learn", ""},
	{"word-ask-me", "ask-me", ""},
	{"model-id", "openai/gpt-oss-120b", ""},
	{"git-sha", "da39a3ee5e6b4b0d3255bfef95601890afd80709", ""},
	{"uuid", "123e4567-e89b-12d3-a456-426614174000", ""},
	{"public-key", "-----BEGIN PUBLIC KEY-----", ""},
}

func TestRedactExtendedPatterns(t *testing.T) {
	for _, tc := range extendedRedactCases {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.input)
			if tc.secret == "" {
				if got != tc.input {
					t.Fatalf("benign input changed: got %q want %q", got, tc.input)
				}
				return
			}
			if strings.Contains(got, tc.secret) {
				t.Fatalf("secret survived redaction: %q", got)
			}
			if !strings.Contains(got, Token) {
				t.Fatalf("replacement token missing: %q", got)
			}
		})
	}
}

func TestRedactIdempotent(t *testing.T) {
	for _, tc := range extendedRedactCases {
		t.Run(tc.name, func(t *testing.T) {
			once := Redact(tc.input)
			if twice := Redact(once); twice != once {
				t.Fatalf("redaction is not idempotent: once=%q twice=%q", once, twice)
			}
		})
	}
	// The replacement token must never itself look like a credential, or each
	// pass would rewrite the previous output and idempotence would be a lie.
	if secretPattern.MatchString(Token) {
		t.Fatalf("replacement token %q is itself matched by the redaction pattern", Token)
	}
}

// TestRedactCombinedMatchesSequential proves the single-alternation form of
// Redact is behavior-preserving: it produces exactly what the literal
// (pre-optimization) shape did — every source compiled and applied one at a
// time, in order — for every case in the table. If a future edit reorders an
// alternative or introduces a pattern that can match an earlier replacement,
// the two shapes diverge here.
func TestRedactCombinedMatchesSequential(t *testing.T) {
	sequential := make([]*regexp.Regexp, len(secretPatternSources))
	for i, src := range secretPatternSources {
		sequential[i] = regexp.MustCompile(src)
	}
	for _, tc := range extendedRedactCases {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.input
			for _, re := range sequential {
				want = re.ReplaceAllString(want, Token)
			}
			if got := Redact(tc.input); got != want {
				t.Fatalf("combined != sequential: combined=%q sequential=%q", got, want)
			}
		})
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
