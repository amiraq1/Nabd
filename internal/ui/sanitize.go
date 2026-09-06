package ui

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// DisplayPolicy controls text sanitization behavior at the display boundary.
type DisplayPolicy struct {
	AllowNewline bool // Multi-line text (assistant response, tool output)
	AllowTab     bool // Tab indentation
	Redact       bool // Redact sensitive credentials (args, errors, notices)
}

const redactedToken = "[REDACTED]"

// Secret patterns for redaction at the display boundary.
var displaySecretPatterns = []*regexp.Regexp{
	// Anthropic
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{8,}`),
	// OpenRouter
	regexp.MustCompile(`sk-or-[A-Za-z0-9_\-]{8,}`),
	// Groq
	regexp.MustCompile(`gsk_[A-Za-z0-9_]{8,}`),
	// NVIDIA
	regexp.MustCompile(`nvapi-[A-Za-z0-9_\-]{8,}`),
	// Bearer authorization
	regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9_\-\.]{8,}`),
	// Authorization headers
	regexp.MustCompile(`(?i)authorization[:\s]+[A-Za-z0-9_\-\.]{8,}`),
	// GitHub personal access tokens
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{16,}`),
	// GitLab personal access tokens
	regexp.MustCompile(`glpat-[A-Za-z0-9_\-]{16,}`),
	// Slack tokens
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9_\-]{10,}`),
}

// ANSI escape sequence patterns:
// CSI (Control Sequence Introducer): ESC [ ... [@-~]
// OSC (Operating System Command): ESC ] ... (BEL | ESC \ | ST)
// DCS, APC, SOS, PM: ESC (P|_|X|^) ... (ESC \ | ST)
// Fe / Fs / Fp escapes: ESC [@-Z\\-_] or ESC [ -/]+ [@-~]
var ansiEscapePattern = regexp.MustCompile(
	`(?s)` +
		`\x1b\[[0-9:;<=>?]*[ -/]*[@-~]` + // CSI
		`|\x1b\][^\x07\x1b\x9c]*(?:\x07|\x1b\\|\x9c|$)` + // OSC (title, clipboard, hyperlinks)
		`|\x1b[P_X^][^\x1b\x9c]*(?:\x1b\\|\x9c|$)` + // DCS, APC, SOS, PM
		`|\x1b[@-Z\\-_]` + // 2-byte Fe escape sequences
		`|\x1b[ -/]+[@-~]` + // 2-byte Fs/Fp/Nf escape sequences
		`|\x1b`, // lone ESC
)

// SanitizeForDisplay sanitizes untrusted text at the display boundary before
// cell width calculation, line wrapping, or application styling.
// Original journal bytes are never altered.
func SanitizeForDisplay(untrusted string, p DisplayPolicy) string {
	if untrusted == "" {
		return ""
	}

	// 1. Redact secrets before any truncation or formatting if requested.
	if p.Redact {
		for _, re := range displaySecretPatterns {
			untrusted = re.ReplaceAllString(untrusted, redactedToken)
		}
	}

	// 2. Remove all ANSI escape sequences completely.
	untrusted = ansiEscapePattern.ReplaceAllString(untrusted, "")

	// 3. Normalize newlines: CRLF -> \n, lone CR removed.
	untrusted = strings.ReplaceAll(untrusted, "\r\n", "\n")
	untrusted = strings.ReplaceAll(untrusted, "\r", "")

	// 4. Character-by-character rune filter:
	// - Replace invalid UTF-8 with U+FFFD.
	// - Strip C0 controls (0x00-0x1F) except \n and \t per policy.
	// - Strip DEL (0x7F) and C1 controls (U+0080-U+009F).
	// - Strip zero-width format controls (U+200B zero-width space, U+200C ZWNJ, U+FEFF BOM).
	//   ZWJ (U+200D) is preserved to keep emoji sequences (e.g. 👨‍👩‍👧‍👦) intact.
	// - Bidi controls: Overrides and isolates (U+202A-U+202E, U+2066-U+2069) are stripped
	//   because they are terminal spoofing vectors (filename/command obfuscation).
	//   LRM (U+200E) and RLM (U+200F) are explicitly preserved for legitimate Arabic/RTL text.
	var b strings.Builder
	b.Grow(len(untrusted))

	for i := 0; i < len(untrusted); {
		r, size := utf8.DecodeRuneInString(untrusted[i:])
		if r == utf8.RuneError && size == 1 {
			// Invalid UTF-8 byte sequence replaced with replacement character.
			b.WriteRune('\uFFFD')
			i += size
			continue
		}
		i += size

		// C0 controls
		if r < 0x20 {
			switch r {
			case '\n':
				if p.AllowNewline {
					b.WriteByte('\n')
				} else {
					b.WriteByte(' ')
				}
			case '\t':
				if p.AllowTab {
					b.WriteString("    ") // 4 spaces for reliable cell alignment
				} else {
					b.WriteByte(' ')
				}
			default:
				// Drop all other C0 controls (NUL, BEL, BS, etc.)
			}
			continue
		}

		// DEL (0x7F) and C1 controls (0x80 - 0x9F)
		if r == 0x7F || (r >= 0x80 && r <= 0x9F) {
			continue
		}

		// Zero-width format controls (except ZWJ)
		if r == 0x200B || r == 0x200C || r == 0xFEFF {
			continue
		}

		// Bidi overrides and isolates (spoofing vectors)
		// U+202A: LRE, U+202B: RLE, U+202C: PDF, U+202D: LRO, U+202E: RLO
		// U+2066: LRI, U+2067: RLI, U+2068: FSI, U+2069: PDI
		if (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) {
			continue
		}

		// Natural Unicode (Arabic, combining marks, emojis, LRM/RLM, Latin, etc.)
		b.WriteRune(r)
	}

	return b.String()
}
