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

func FuzzStreamSecretsNeverSurface(f *testing.F) {
	f.Add([]byte{10, 20, 50})
	f.Add([]byte{1, 2, 3, 4, 5})
	f.Add([]byte{64, 128})
	f.Fuzz(func(t *testing.T, cuts []byte) {
		const (
			secretToken = "TOPSECRETKEYBODY"
			secretTail  = "STILLSECRETTAIL"
		)
		pem := "-----BEGIN RSA PRIVATE KEY-----\n" + secretToken + "\n" + secretTail + "\n-----END RSA PRIVATE KEY-----\n"
		input := strings.Repeat("X", StreamHoldCap+500) + pem + strings.Repeat("Y", StreamHoldCap+500)

		s := NewStream(nil)
		var out strings.Builder
		pos := 0
		for _, c := range cuts {
			step := int(c)%256 + 1
			if pos+step > len(input) {
				step = len(input) - pos
			}
			if step > 0 {
				out.WriteString(s.Write(input[pos : pos+step]))
				pos += step
			}
		}
		if pos < len(input) {
			out.WriteString(s.Write(input[pos:]))
		}
		out.WriteString(s.Flush())
		got := out.String()

		if strings.Contains(got, secretToken) {
			t.Fatalf("secret token surfaced: %q", got)
		}
		if strings.Contains(got, secretTail) {
			t.Fatalf("secret tail surfaced: %q", got)
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

func TestStreamSwallowPreservesPEMBoundary(t *testing.T) {
	s := NewStream(nil)
	var out strings.Builder
	out.WriteString(s.Write(strings.Repeat("A", 5000)))
	out.WriteString(s.Write("-----BEGIN RSA PRIVATE KEY-----\nMIIEsecretbody\n"))
	out.WriteString(s.Write("moresecret\n-----END RSA PRIVATE KEY-----\n"))
	out.WriteString(s.Flush())
	got := out.String()

	if strings.Contains(got, "secretbody") {
		t.Fatalf("output contains secretbody: %q", got)
	}
	if strings.Contains(got, "moresecret") {
		t.Fatalf("output contains moresecret: %q", got)
	}
	last := strings.LastIndex(got, Token)
	if last < 0 || strings.Contains(got[last+len(Token):], "A") || strings.Contains(strings.ReplaceAll(got, Token, ""), "A") {
		t.Fatalf("output contains 'A' after token: %q", got)
	}
}

func TestStreamPEMHoldCapOpenBlock(t *testing.T) {
	s := NewStream(nil)
	var out strings.Builder
	head := "-----BEGIN RSA PRIVATE KEY-----\n"
	body := strings.Repeat("A", 1024*1024)
	chunkSize := 1024
	input := head + body
	for i := 0; i < len(input); i += chunkSize {
		end := i + chunkSize
		if end > len(input) {
			end = len(input)
		}
		out.WriteString(s.Write(input[i:end]))
		if s.Pending() > StreamPEMHoldCap {
			t.Fatalf("pending exceeded StreamPEMHoldCap: %d > %d", s.Pending(), StreamPEMHoldCap)
		}
	}
	out.WriteString(s.Flush())
	got := out.String()
	if strings.Contains(got, "AAAA") {
		t.Fatalf("body byte surfaced: %q", got)
	}
	if !strings.Contains(got, Token) {
		t.Fatalf("expected Token in output: %q", got)
	}
}

func TestStreamPEMValidBlockMatchesRedact(t *testing.T) {
	block := "-----BEGIN RSA PRIVATE KEY-----\n" +
		strings.Repeat("MIIEowIBAAKCAQEA0123456789abcdefghijklmnopqrstuvwxyz\n", 58) +
		"-----END RSA PRIVATE KEY-----\n"
	if len(block) < 3000 || len(block) > 3600 {
		t.Fatalf("unexpected test block size: %d", len(block))
	}
	want := Redact(block)
	got := streamed(block, 10, 50, 100, 500, 1500, 2500)
	if got != want {
		t.Fatalf("streamed != Redact: got %q, want %q", got, want)
	}
}

func TestStreamSwallowHoldsAcrossDashBoundary(t *testing.T) {
	s := NewStream(nil)
	var out strings.Builder
	out.WriteString(s.Write(strings.Repeat("A", 5000)))
	out.WriteString(s.Write("AAAA-"))
	out.WriteString(s.Write("tailsecret more "))
	out.WriteString(s.Flush())
	got := out.String()

	if strings.Contains(got, "tailsecret") {
		t.Fatalf("output contains tailsecret: %q", got)
	}
}

func TestStreamPEMSwallowFindsSplitEnd(t *testing.T) {
	s := NewStream(nil)
	var out strings.Builder
	head := "-----BEGIN RSA PRIVATE KEY-----\n"
	body := "bodysecret" + strings.Repeat("A", 9000-len("bodysecret"))
	out.WriteString(s.Write(head + body))
	out.WriteString(s.Write("\n---"))
	out.WriteString(s.Write("--END RSA PRIVATE KEY-----\nvisible text"))
	out.WriteString(s.Flush())
	got := out.String()

	if strings.Contains(got, "bodysecret") {
		t.Fatalf("output contains bodysecret: %q", got)
	}
	if !strings.Contains(got, "visible text") {
		t.Fatalf("expected visible text in output: %q", got)
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
