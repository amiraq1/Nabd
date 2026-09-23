package redact

import (
	"regexp"
	"strings"
)

// StreamHoldCap bounds how many unemitted bytes a Stream keeps while waiting to
// see whether a trailing run becomes a credential. A credential is short; a
// trailing run of token characters longer than this is not prose, so the cap
// converts it to a Token and keeps swallowing the rest of the run rather than
// growing without bound. It matches MaxBodyBytes so a single held field cannot
// exceed the provider-body storage bound.
const StreamHoldCap = MaxBodyBytes

// pemBeginMarker and pemEndMarker are the two halves of the PEM private-key
// shape, compiled on their own so the stream can tell a complete block from an
// unterminated one that it must hold to end of input.
var (
	pemBeginMarker = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)
	pemEndMarker   = regexp.MustCompile(`-----END [A-Z ]*PRIVATE KEY-----`)
)

// Stream redacts credentials from text that arrives in chunks, so a credential
// or PEM block split across two chunks is redacted as if it had arrived whole.
//
// The core property, for any input x and any split into chunks, is that the
// concatenation of every Write result plus Flush equals Redact of x (after the
// exact-key substitution a sink applies). Write must therefore hold back any
// trailing run that could still grow into or extend a match, and Flush releases
// it, redacted.
//
// Hold-back is bounded (StreamHoldCap) and minimal: only a trailing run of token
// characters, a trailing Bearer/authorization keyword context, an exact-key
// prefix, or an open PEM block is withheld. Ordinary prose ending in whitespace
// or punctuation is emitted at once.
//
// A Stream is not safe for concurrent use: it carries the partial-match state
// for one streamed field, and callers must use one goroutine per stream.
type Stream struct {
	exact   []string
	pending string
	// swallow is set when the cap forced a partial emit while inside an
	// over-long token run. The rest of that run must not surface, so Write drops
	// token bytes until the run ends.
	swallow bool
}

// NewStream returns a Stream that also redacts the given exact credential
// values. Empty values are ignored, matching RedactExactKeys.
func NewStream(exactKeys []string) *Stream {
	keys := make([]string, 0, len(exactKeys))
	for _, k := range exactKeys {
		if k != "" {
			keys = append(keys, k)
		}
	}
	return &Stream{exact: keys}
}

// Write consumes one chunk and returns the text that is now safe to emit,
// already redacted. It may return "" when the whole chunk must be held or
// swallowed; the caller then emits no event for this chunk.
func (s *Stream) Write(chunk string) (emit string) {
	s.pending += chunk

	// An open PEM block is held in full: its interior is arbitrary bytes and the
	// only safe boundary is the matching END line or Flush. Everything before the
	// BEGIN is already decidable, so it is emitted now.
	if b := openPEMStart(s.pending); b >= 0 {
		if b > 0 {
			emit = s.redact(s.pending[:b])
			s.pending = s.pending[b:]
		}
		return emit
	}

	// Finish swallowing an over-long token run before looking for new holds.
	if s.swallow {
		k := 0
		for k < len(s.pending) && isTokenByte(s.pending[k]) {
			k++
		}
		s.pending = s.pending[k:]
		s.swallow = false
		if s.pending == "" {
			return emit
		}
	}

	hold := s.holdBackLen()
	n := len(s.pending)
	if hold > StreamHoldCap {
		runStart := n
		for runStart > 0 && isTokenByte(s.pending[runStart-1]) {
			runStart--
		}
		if n-runStart > StreamHoldCap {
			// The trailing run alone is over the cap. Emit the decidable prefix,
			// replace the run's start with Token, and swallow the remainder so the
			// tail of a long key never surfaces without redaction.
			emit += s.redact(s.pending[:runStart]) + Token
			s.pending = s.pending[runStart:]
			s.swallow = true
			return emit
		}
		hold = StreamHoldCap
	}

	cut := n - hold
	cut = clampPEMCut(s.pending, cut)
	if cut <= 0 {
		return emit
	}
	emit += s.redact(s.pending[:cut])
	s.pending = s.pending[cut:]
	return emit
}

// Pending reports how many bytes are held and not yet emitted.
func (s *Stream) Pending() int { return len(s.pending) }

// Swallowing reports whether the stream is discarding the tail of an over-cap
// token run.
func (s *Stream) Swallowing() bool { return s.swallow }

// PushBack returns previously emitted text to the hold. A caller that decides to
// withhold a prefix — to keep a sequence number free for a later flush — uses
// this to preserve byte order.
func (s *Stream) PushBack(prefix string) {
	if prefix == "" {
		return
	}
	s.pending = prefix + s.pending
}

// Flush releases whatever is held, redacted. Call it on every terminal path so
// held bytes are never dropped and never emitted raw. After Flush the stream is
// empty and may be reused for the next message.
func (s *Stream) Flush() string {
	if s.swallow {
		// The run's start was already emitted as Token; the swallowed tail is
		// deliberately discarded rather than surfaced.
		s.pending = ""
		s.swallow = false
		return ""
	}
	out := s.redact(s.pending)
	s.pending = ""
	return out
}

func (s *Stream) redact(text string) string {
	return Redact(RedactExactKeys(text, s.exact))
}

// clampPEMCut moves a cut that would fall inside a complete PEM block back to
// the block's start. A block emitted in halves is unsafe: the head, missing its
// END line, matches the unterminated alternative and is redacted, while the END
// line's tail would leak. Holding the whole block keeps it whole for the one
// pass that redacts it.
func clampPEMCut(pending string, cut int) int {
	begins := pemBeginMarker.FindAllStringIndex(pending, -1)
	if len(begins) == 0 {
		return cut
	}
	last := begins[len(begins)-1]
	rest := pending[last[1]:]
	loc := pemEndMarker.FindStringIndex(rest)
	if loc == nil {
		return cut
	}
	end := last[1] + loc[1]
	if cut > last[0] && cut < end {
		return last[0]
	}
	return cut
}

// holdBackLen returns the number of trailing bytes of pending that could still
// grow into or extend a match and must therefore be withheld.
func (s *Stream) holdBackLen() int {
	n := len(s.pending)
	i := n

	// A trailing run of token characters can extend any prefixed credential.
	for i > 0 && isTokenByte(s.pending[i-1]) {
		i--
	}

	// A keyword context ("Bearer", "authorization", with any separator run)
	// starts a match whose secret may not have arrived yet.
	j := i
	for j > 0 && isSepByte(s.pending[j-1]) {
		j--
	}
	if k := keywordEndsAt(s.pending[:j]); k >= 0 {
		i = k
	}

	// An exact configured key can arrive in pieces; hold any suffix that is a
	// prefix of one.
	for _, key := range s.exact {
		if l := trailingPrefixOf(s.pending, key); l > 0 {
			if start := n - l; start < i {
				i = start
			}
		}
	}

	return n - i
}

// openPEMStart returns the index at which an unterminated PEM private-key block
// begins, or -1. Partial BEGIN markers that end in a letter or dash are already
// held by the trailing token run in holdBackLen; this handles the rest of the
// marker, including a space after BEGIN, by holding from the BEGIN keyword.
func openPEMStart(pending string) int {
	b := strings.LastIndex(pending, "-----BEGIN")
	if b < 0 {
		return -1
	}
	rest := pending[b+len("-----BEGIN"):]
	dashes := 0
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		switch {
		case c == '-':
			dashes++
			if dashes == 5 {
				marker := pending[b : b+len("-----BEGIN")+i+1]
				if !strings.Contains(marker, "PRIVATE KEY") {
					return -1
				}
				if pemEndMarker.MatchString(pending[b+len("-----BEGIN")+i+1:]) {
					return -1
				}
				return b
			}
		case c == ' ' || (c >= 'A' && c <= 'Z'):
			dashes = 0
		default:
			return -1
		}
	}
	// The marker is not finished yet; hold it until it is or until Flush.
	return b
}

// trailingPrefixOf returns the length L (0..len(m)) of the longest suffix of s
// that equals a prefix of m.
func trailingPrefixOf(s, m string) int {
	max := len(m)
	if max > len(s) {
		max = len(s)
	}
	for l := max; l > 0; l-- {
		if s[len(s)-l:] == m[:l] {
			return l
		}
	}
	return 0
}

// keywordEndsAt returns the index at which a Bearer/authorization keyword ends
// the string s, case-insensitively, or -1.
func keywordEndsAt(s string) int {
	ls := strings.ToLower(s)
	if strings.HasSuffix(ls, "bearer") {
		return len(s) - len("bearer")
	}
	if strings.HasSuffix(ls, "authorization") {
		return len(s) - len("authorization")
	}
	return -1
}

// isTokenByte reports whether b is one of the characters that can appear inside
// a credential token matched by secretPattern.
func isTokenByte(b byte) bool {
	return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' ||
		b == '_' || b == '-' || b == '.'
}

// isSepByte reports whether b is a separator allowed between a Bearer/
// authorization keyword and its token.
func isSepByte(b byte) bool {
	return b == ':' || b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}
