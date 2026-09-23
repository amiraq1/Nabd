package redact

import (
	"math/rand"
	"strings"
	"testing"
)

// streamed feeds input through a Stream split at the given byte offsets and
// returns everything it emitted, including the final Flush.
func streamed(input string, cuts ...int) string {
	s := NewStream(nil)
	var b strings.Builder
	prev := 0
	for _, c := range cuts {
		b.WriteString(s.Write(input[prev:c]))
		prev = c
	}
	b.WriteString(s.Write(input[prev:]))
	b.WriteString(s.Flush())
	return b.String()
}

// TestStreamMatchesWholeEverySplit is the core property: for every case in the
// redaction table, every single split point and every pair of split points must
// produce exactly what whole-input Redact produces.
func TestStreamMatchesWholeEverySplit(t *testing.T) {
	for _, tc := range extendedRedactCases {
		t.Run(tc.name, func(t *testing.T) {
			want := Redact(tc.input)
			n := len(tc.input)
			for i := 0; i <= n; i++ {
				if got := streamed(tc.input, i); got != want {
					t.Fatalf("single split at %d: got %q want %q", i, got, want)
				}
			}
			for i := 0; i <= n; i++ {
				for j := i; j <= n; j++ {
					if got := streamed(tc.input, i, j); got != want {
						t.Fatalf("splits %d,%d: got %q want %q", i, j, got, want)
					}
				}
			}
		})
	}
}

// TestStreamRandomChunking checks the same property under random chunk sizes
// from 1 to 64, with a fixed seed so a failure is reproducible.
func TestStreamRandomChunking(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	inputs := make([]string, 0, len(extendedRedactCases)+2)
	for _, tc := range extendedRedactCases {
		inputs = append(inputs, tc.input)
	}
	inputs = append(inputs,
		"prose then a key sk-proj-abcdefghijklmnopqrstuvwxyz0123456789 then prose",
		"plain sentence with no secrets at all, just words and punctuation.",
	)
	for _, in := range inputs {
		want := Redact(in)
		for iter := 0; iter < 200; iter++ {
			s := NewStream(nil)
			var b strings.Builder
			for i := 0; i < len(in); {
				sz := rng.Intn(64) + 1
				if i+sz > len(in) {
					sz = len(in) - i
				}
				b.WriteString(s.Write(in[i : i+sz]))
				i += sz
			}
			b.WriteString(s.Flush())
			if got := b.String(); got != want {
				t.Fatalf("input %q iter %d: got %q want %q", in, iter, got, want)
			}
		}
	}
}

func FuzzStreamMatchesWhole(f *testing.F) {
	f.Add("before Bearer abcdefgh12345678 after")
	f.Add("-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA")
	f.Add("sk-proj-abcdefghijklmnopqrstuvwxyz0123456789")
	f.Add("authorization: supersecrettoken value")
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 4096 {
			s = s[:4096]
		}
		want := Redact(s)
		for i := 0; i <= len(s); i++ {
			if got := streamed(s, i); got != want {
				t.Fatalf("split at %d: got %q want %q (input %q)", i, got, want, s)
			}
		}
	})
}

// TestStreamEmitsProseImmediately pins that plain prose ending in whitespace is
// not withheld.
func TestStreamEmitsProseImmediately(t *testing.T) {
	s := NewStream(nil)
	if got := s.Write("hello world "); got != "hello world " {
		t.Fatalf("prose withheld: got %q want the whole chunk", got)
	}
	if got := s.Flush(); got != "" {
		t.Fatalf("flush after an immediate emit: got %q want empty", got)
	}
}

// TestStreamCapContinuationSwallowed pins the cap behaviour: a token run longer
// than the cap is redacted and its tail swallowed, so no raw fragment of the run
// survives and the stream does not grow without bound.
func TestStreamCapContinuationSwallowed(t *testing.T) {
	run := "sk-proj-" + strings.Repeat("a", StreamHoldCap*2)
	s := NewStream(nil)
	var b strings.Builder
	b.WriteString(s.Write("lead "))
	b.WriteString(s.Write(run))
	b.WriteString(s.Write("tail"))
	b.WriteString(s.Flush())
	got := b.String()

	if strings.Contains(got, strings.Repeat("a", 64)) {
		t.Fatalf("long token run leaked raw: %q", got)
	}
	if !strings.Contains(got, Token) {
		t.Fatalf("cap overflow did not redact: %q", got)
	}
	if !strings.HasPrefix(got, "lead ") {
		t.Fatalf("text before the run was lost: %q", got)
	}
}

// TestStreamUnterminatedPEMRedactedOnFlush pins that a BEGIN line with no END is
// held and then redacted from BEGIN to the end of input, matching Redact.
func TestStreamUnterminatedPEMRedactedOnFlush(t *testing.T) {
	input := "before -----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjE after"
	want := Redact(input)
	got := streamed(input, 5, 20, 40)
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if strings.Contains(got, "-----BEGIN OPENSSH PRIVATE KEY-----") || strings.Contains(got, "b3BlbnNzaC1rZXktdjE") {
		t.Fatalf("PEM leaked: %q", got)
	}
}

func BenchmarkRedact(b *testing.B) {
	s := "Bash echo hello world this is a tool summary line with no secrets here"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Redact(s)
	}
}

func BenchmarkStreamWrite(b *testing.B) {
	chunk := "Bash echo hello world this is a tool summary line "
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s := NewStream(nil)
		_ = s.Write(chunk)
		_ = s.Flush()
	}
}
