package redact

import (
	"fmt"
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

// TestStreamCutNeverSplitsCompleteMatch ensures a complete match is never split
// across an emitted prefix and a held suffix.
func TestStreamCutNeverSplitsCompleteMatch(t *testing.T) {
	cases := []string{
		"BeArer 00BeArer ",
		"Bearer abcdefgauthorization ",
		"authorization: 1234567Bearer\n",
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			want := Redact(input)
			n := len(input)
			for i := 0; i <= n; i++ {
				if got := streamed(input, i); got != want {
					t.Fatalf("single split at %d for %q: got %q want %q", i, input, got, want)
				}
			}
			for i := 0; i <= n; i++ {
				for j := i; j <= n; j++ {
					if got := streamed(input, i, j); got != want {
						t.Fatalf("splits %d,%d for %q: got %q want %q", i, j, input, got, want)
					}
				}
			}
		})
	}
}

// TestStreamCutNeverSplitsExactKey ensures an exact key match is never split
// across an emitted prefix and a held suffix.
func TestStreamCutNeverSplitsExactKey(t *testing.T) {
	exactKeys := []string{"CUSTOM_KEY_12345Bearer ", "overlap-key-suffix"}
	for _, k := range exactKeys {
		t.Run(k, func(t *testing.T) {
			want := Redact(RedactExactKeys(k, exactKeys))
			n := len(k)
			for i := 0; i <= n; i++ {
				s := NewStream(exactKeys)
				got := s.Write(k[:i]) + s.Write(k[i:]) + s.Flush()
				if got != want {
					t.Fatalf("single split at %d for %q: got %q want %q", i, k, got, want)
				}
			}
			for i := 0; i <= n; i++ {
				for j := i; j <= n; j++ {
					s := NewStream(exactKeys)
					got := s.Write(k[:i]) + s.Write(k[i:j]) + s.Write(k[j:]) + s.Flush()
					if got != want {
						t.Fatalf("splits %d,%d for %q: got %q want %q", i, j, k, got, want)
					}
				}
			}
		})
	}
}

// TestStreamPendingBoundedOnLongBearerChain verifies that streaming a long
// repetition of Bearer tokens does not grow the held buffer without bound,
// keeping Pending() <= StreamHoldCap within a match length margin.
func TestStreamPendingBoundedOnLongBearerChain(t *testing.T) {
	unit := "Bearer 00BeArer "
	const count = 300
	longInput := strings.Repeat(unit, count)
	want := Redact(longInput)

	// Chunk-by-chunk write
	s := NewStream(nil)
	var out strings.Builder
	for i := 0; i < count; i++ {
		out.WriteString(s.Write(unit))
		if p := s.Pending(); p > StreamHoldCap {
			t.Fatalf("pending %d exceeded StreamHoldCap %d at iteration %d", p, StreamHoldCap, i)
		}
		if p := s.Pending(); p > len(unit)*2 {
			t.Fatalf("pending %d exceeded match margin %d at iteration %d", p, len(unit)*2, i)
		}
	}
	out.WriteString(s.Flush())
	if got := out.String(); got != want {
		t.Fatalf("chunk-by-chunk got != want: got len %d want len %d", len(got), len(want))
	}

	// Byte-by-byte write
	s2 := NewStream(nil)
	var out2 strings.Builder
	for i := 0; i < len(longInput); i++ {
		out2.WriteString(s2.Write(longInput[i : i+1]))
		if p := s2.Pending(); p > StreamHoldCap {
			t.Fatalf("byte-by-byte pending %d exceeded StreamHoldCap %d at byte %d", p, StreamHoldCap, i)
		}
		if p := s2.Pending(); p > len(unit)*2 {
			t.Fatalf("byte-by-byte pending %d exceeded match margin %d at byte %d", p, len(unit)*2, i)
		}
	}
	out2.WriteString(s2.Flush())
	if got := out2.String(); got != want {
		t.Fatalf("byte-by-byte got != want: got len %d want len %d", len(got), len(want))
	}
}

// TestStreamLongBearerTokenStaysCapped verifies the clamp cannot grow the hold
// without bound. The clamp moves a cut back to the start of a straddling match,
// so a single keyword-prefixed token run far longer than StreamHoldCap is the
// adversarial case: if the cut were clamped inside a match that spans the whole
// buffer, Pending() would grow with the run instead of staying capped. A run
// over the cap must be emitted as one Token by the pre-clamp guard and its
// remainder swallowed, keeping Pending() at O(StreamHoldCap).
//
// It deliberately does not assert that the streamed text equals whole-input
// Redact. An over-cap run behind a keyword is a known, pre-existing divergence:
// the swallow path emits the literal keyword (e.g. "Bearer [REDACTED]" where
// Redact produces "[REDACTED]"). The secret body never surfaces, so this test
// pins the bound and the absence of a raw run instead of the exact wording.
func TestStreamLongBearerTokenStaysCapped(t *testing.T) {
	input := "Bearer " + strings.Repeat("A", 1<<20)

	for _, chunk := range []int{64, StreamHoldCap} {
		t.Run(fmt.Sprintf("chunk=%d", chunk), func(t *testing.T) {
			s := NewStream(nil)
			var out strings.Builder
			max := 0
			for i := 0; i < len(input); {
				n := chunk
				if i+n > len(input) {
					n = len(input) - i
				}
				out.WriteString(s.Write(input[i : i+n]))
				if p := s.Pending(); p > max {
					max = p
				}
				i += n
			}
			out.WriteString(s.Flush())

			if max > 2*StreamHoldCap {
				t.Fatalf("pending grew to %d bytes, over the 2*StreamHoldCap bound (%d)", max, 2*StreamHoldCap)
			}
			if strings.Contains(out.String(), "AAAA") {
				t.Fatalf("token run leaked into the emitted stream")
			}
			if !strings.Contains(out.String(), Token) {
				t.Fatalf("emitted stream %q carries no redaction token", out.String())
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

// TestStreamOverCapTokenRunsAndPEMEverySplit mixes over-cap token runs with PEM
// blocks across all single split points and representative pairwise splits,
// asserting that neither the secret body nor the over-cap token run leaks.
func TestStreamOverCapTokenRunsAndPEMEverySplit(t *testing.T) {
	const pemSecret = "SUPERSECRETKEYBODY123456789"
	pemBlock := "-----BEGIN RSA PRIVATE KEY-----\n" + pemSecret + "\n-----END RSA PRIVATE KEY-----\n"
	overCapRun := "sk-proj-" + strings.Repeat("q", StreamHoldCap+64)

	testCases := []struct {
		name  string
		input string
	}{
		{
			name:  "overcap-then-pem",
			input: "lead " + overCapRun + " mid " + pemBlock + " tail",
		},
		{
			name:  "pem-then-overcap",
			input: "lead " + pemBlock + " mid " + overCapRun + " tail",
		},
		{
			name:  "adjacent-overcap-pem",
			input: overCapRun + "\n" + pemBlock,
		},
		{
			name:  "adjacent-pem-overcap",
			input: pemBlock + overCapRun,
		},
		{
			name:  "overcap-then-unterminated-pem",
			input: overCapRun + "\n-----BEGIN RSA PRIVATE KEY-----\n" + pemSecret,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assertNoLeak := func(splitDesc string, got string) {
				if strings.Contains(got, pemSecret) {
					t.Fatalf("%s: PEM secret leaked: %q", splitDesc, got)
				}
				if strings.Contains(got, strings.Repeat("q", 32)) {
					t.Fatalf("%s: over-cap token tail leaked: %q", splitDesc, got)
				}
				if strings.Contains(got, "-----BEGIN RSA PRIVATE KEY-----") {
					t.Fatalf("%s: PEM marker leaked raw: %q", splitDesc, got)
				}
			}

			n := len(tc.input)
			// Test single split points: dense around boundaries and key transitions,
			// stepped across uniform over-cap runs to keep test execution bounded.
			splits := make(map[int]bool)
			for i := 0; i <= n; i++ {
				if i < 128 || i > n-128 || (i >= StreamHoldCap-64 && i <= StreamHoldCap+64) || i%32 == 0 {
					splits[i] = true
				}
			}
			if idx := strings.Index(tc.input, "-----BEGIN"); idx >= 0 {
				end := idx + 64
				if end > n {
					end = n
				}
				for i := idx; i <= end; i++ {
					splits[i] = true
				}
			}
			for i := range splits {
				got := streamed(tc.input, i)
				assertNoLeak("single split", got)
			}

			// Test representative two-cut splits stepped across the input:
			step := n / 25
			if step < 1 {
				step = 1
			}
			for i := 0; i <= n; i += step {
				for j := i; j <= n; j += step {
					got := streamed(tc.input, i, j)
					assertNoLeak("pairwise split", got)
				}
			}
		})
	}
}

// TestStreamPEMLeakViaSwallowInteraction_S1 is a regression test for the bug where
// openPEMStart was previously checked before s.swallow in Write:
//  1. Chunk 1 has a token run exceeding StreamHoldCap (emits Token, sets swallow=true).
//  2. Chunk 2 starts with an open PEM block (no END). If openPEMStart was checked
//     before swallow, redact(pending[:b]) would emit the swallowed token tail raw,
//     while swallow remained true.
//  3. Chunk 3 provides the PEM END line. openPEMStart returns -1, so swallow would run
//     on the pending PEM block, stripping "-----BEGIN" (10 token bytes) up to the space.
//     Missing "-----BEGIN", the private key body matched no secret pattern and leaked raw.
func TestStreamPEMLeakViaSwallowInteraction_S1(t *testing.T) {
	s := NewStream(nil)
	var out strings.Builder

	tokenTail := strings.Repeat("x", StreamHoldCap+50)
	chunk1 := tokenTail
	out.WriteString(s.Write(chunk1))

	keyBody := "MIIEowIBAAKCAQEA0Y1234567890abcdefghijklmnopqrstuvwxyzSECRETKEYBODY"
	chunk2 := "-----BEGIN RSA PRIVATE KEY-----\n" + keyBody + "\n"
	out.WriteString(s.Write(chunk2))

	chunk3 := "-----END RSA PRIVATE KEY-----\n"
	out.WriteString(s.Write(chunk3))
	out.WriteString(s.Flush())

	got := out.String()

	// 1. Neither the PEM key body nor the swallowed token tail must appear.
	if strings.Contains(got, keyBody) {
		t.Fatalf("S1 leak reproduced: PEM private key body leaked in output: %q", got)
	}
	if strings.Contains(got, "SECRETKEYBODY") {
		t.Fatalf("S1 leak reproduced: PEM secret fragment leaked in output: %q", got)
	}
	if strings.Contains(got, strings.Repeat("x", 32)) {
		t.Fatalf("S1 leak reproduced: tail of over-cap token run leaked in output: %q", got)
	}

	// 2. No token bytes from the over-cap run must appear after Token.
	idx := strings.Index(got, Token)
	if idx < 0 {
		t.Fatalf("expected Token in output, got: %q", got)
	}
	afterToken := got[idx+len(Token):]
	if strings.Contains(afterToken, "xxxx") {
		t.Fatalf("S1 leak reproduced: token bytes from over-cap run appeared after Token: %q", afterToken)
	}
}

// TestStreamPEMHoldCapOpenBlockBoundedAndRedacted tests that an open PEM block
// exceeding StreamPEMHoldCap triggers fail-closed redaction: Token is emitted,
// pending buffer growth is strictly bounded, and no interior key bytes leak.
func TestStreamPEMHoldCapOpenBlockBoundedAndRedacted(t *testing.T) {
	keySecret := "SUPERSECRETKEYBODY-RSA4096-DATA"
	hugeKeyBody := strings.Repeat(keySecret+"\n", 500) // ~16KB, well over 8KB cap
	openPEM := "-----BEGIN RSA PRIVATE KEY-----\n" + hugeKeyBody

	s := NewStream(nil)
	var out strings.Builder

	chunkSize := 512
	for i := 0; i < len(openPEM); i += chunkSize {
		end := i + chunkSize
		if end > len(openPEM) {
			end = len(openPEM)
		}
		chunk := openPEM[i:end]
		out.WriteString(s.Write(chunk))

		// Invariant: pending must never grow without bound beyond StreamPEMHoldCap + chunkSize
		if p := s.Pending(); p > StreamPEMHoldCap+chunkSize {
			t.Fatalf("pending grew without bound: %d bytes (cap %d)", p, StreamPEMHoldCap)
		}
	}

	// Close the block and add trailing prose
	out.WriteString(s.Write("-----END RSA PRIVATE KEY-----\nnormal prose after key"))
	out.WriteString(s.Flush())

	got := out.String()

	if strings.Contains(got, keySecret) {
		t.Fatalf("key body leaked in output: %q", got)
	}
	if !strings.Contains(got, Token) {
		t.Fatalf("expected Token in output, got: %q", got)
	}
	if !strings.Contains(got, "normal prose after key") {
		t.Fatalf("prose after PEM block was lost: %q", got)
	}
	if s.Pending() != 0 {
		t.Fatalf("pending not empty after Flush: %d", s.Pending())
	}
}

// TestStreamPEMHoldCapIncompleteMarkerBounded tests that an incomplete PEM BEGIN
// marker that never terminates does not cause unbounded pending buffer growth.
func TestStreamPEMHoldCapIncompleteMarkerBounded(t *testing.T) {
	incompleteMarker := "-----BEGIN " + strings.Repeat("UNTERMINATED PEM MARKER KEY WORDS ", 400) // ~13KB > 8KB

	s := NewStream(nil)
	var out strings.Builder

	chunkSize := 256
	for i := 0; i < len(incompleteMarker); i += chunkSize {
		end := i + chunkSize
		if end > len(incompleteMarker) {
			end = len(incompleteMarker)
		}
		out.WriteString(s.Write(incompleteMarker[i:end]))

		if p := s.Pending(); p > StreamPEMHoldCap+chunkSize {
			t.Fatalf("incomplete marker caused unbounded pending: %d bytes", p)
		}
	}
	out.WriteString(s.Flush())

	got := out.String()
	if strings.Contains(got, "UNTERMINATED") {
		t.Fatalf("raw bytes leaked from over-cap incomplete marker: %q", got)
	}
	if strings.Contains(got, "MARKER KEY WORDS") {
		t.Fatalf("raw bytes leaked from over-cap incomplete marker: %q", got)
	}
	if !strings.Contains(got, Token) {
		t.Fatalf("expected Token for over-cap marker, got: %q", got)
	}
}

// TestStreamPEMWithinCapMatchesRedact verifies that a realistic RSA-4096 PEM block
// (~3.3KB), which is within StreamPEMHoldCap (8KB), matches whole-input Redact
// exactly across various split chunk sizes.
func TestStreamPEMWithinCapMatchesRedact(t *testing.T) {
	// Realistic ~3.3KB RSA-4096 private key:
	rsa4096 := "-----BEGIN RSA PRIVATE KEY-----\n" +
		strings.Repeat("MIIJKAIBAAKCAgEA0Y5l6m7n8o9p1q2r3s4t5u6v7w8x9y0zabcdefghijklm=\n", 50) +
		"-----END RSA PRIVATE KEY-----\n"

	want := Redact(rsa4096)

	chunkSizes := []int{32, 128, 512, 1024, 2048, 4096}
	for _, sz := range chunkSizes {
		s := NewStream(nil)
		var out strings.Builder
		for i := 0; i < len(rsa4096); i += sz {
			end := i + sz
			if end > len(rsa4096) {
				end = len(rsa4096)
			}
			out.WriteString(s.Write(rsa4096[i:end]))
		}
		out.WriteString(s.Flush())
		if got := out.String(); got != want {
			t.Fatalf("chunk size %d: got %q want %q", sz, got, want)
		}
	}

	// Also verify unterminated PEM block under 8KB flushed at EOF matches Redact:
	unterminated := "-----BEGIN RSA PRIVATE KEY-----\n" + strings.Repeat("MIIEowIBAAKCAQEA", 100) // ~1.6KB
	wantUnterminated := Redact(unterminated)
	gotUnterminated := streamed(unterminated, 200, 800)
	if gotUnterminated != wantUnterminated {
		t.Fatalf("unterminated under cap: got %q want %q", gotUnterminated, wantUnterminated)
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
