package ui

import (
	"errors"
	"os"
	"strings"
	"sync"

	"nabd/internal/rtl"

	"github.com/charmbracelet/x/ansi"
)

const (
	styleNone uint16 = iota
	styleBold
)

// NABD_RTL values:
//
//	unset, "", "auto"                 Reorder if stdout is a TTY, else Logical
//	"reorder", "on", "1", "true"      always Reorder
//	"mirror", "reorder-and-mirror"    always ReorderAndMirror
//	"off", "logical", "no", "disable",
//	"0", "false", "none"              Logical (no reordering)
//	anything else                     Logical (never reorder by accident)
//
// Logical output when piped keeps saved logs searchable and copyable.
func parseRTLMode(val string, isTTY bool) rtl.Mode {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "mirror", "reorder-and-mirror":
		return rtl.ReorderAndMirror
	case "reorder", "on", "1", "true":
		return rtl.Reorder
	case "", "auto":
		if isTTY {
			return rtl.Reorder
		}
		return rtl.Logical
	case "off", "logical", "no", "disable", "0", "false", "none":
		return rtl.Logical
	default:
		return rtl.Logical
	}
}

// stdoutIsTTY reports whether stdout is a character device.
// Dependency-free; swap for go-isatty if you want stricter detection.
func stdoutIsTTY() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// readRTLMode reads the environment every call (used by tests).
func readRTLMode() rtl.Mode {
	return parseRTLMode(os.Getenv("NABD_RTL"), stdoutIsTTY())
}

var (
	rtlModeOnce  sync.Once
	rtlModeValue rtl.Mode
)

// rtlDisplayMode resolves the mode once per process.
func rtlDisplayMode() rtl.Mode {
	rtlModeOnce.Do(func() { rtlModeValue = readRTLMode() })
	return rtlModeValue
}

// resetRTLModeCache clears the cached mode. Tests only: call after
// t.Setenv("NABD_RTL", ...) so the new value takes effect.
func resetRTLModeCache() {
	rtlModeOnce = sync.Once{}
}

func rtlDisplayPolicy() rtl.Policy {
	return rtl.Policy{Mode: rtlDisplayMode(), Base: rtl.Auto}
}

// layoutRenderedText is the only UI-to-RTL adapter. logical and spans contain
// no ANSI; styles are emitted after layout from Run.StyleID.
func layoutRenderedText(logical string, spans []rtl.Span, width int, policy rtl.Policy) ([]string, error) {
	lines, err := rtl.Layout(logical, spans, width, policy)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		var b strings.Builder
		for _, run := range line.Runs {
			var text strings.Builder
			for _, cluster := range run.Clusters {
				text.WriteString(cluster.Text)
			}
			b.WriteString(renderStyleID(run.StyleID, text.String()))
		}
		out = append(out, b.String())
	}
	return out, nil
}

func renderStyleID(id uint16, text string) string {
	switch id {
	case styleNone:
		return text
	case styleBold:
		return bold.Render(text)
	default:
		// Unknown style IDs fail closed: preserve text without inventing ANSI.
		return text
	}
}

// wrapLogicalText applies the configured BiDi policy to unstyled logical
// source. Invalid layout input falls back to the existing ANSI-aware wrapper;
// rendering must never panic or drop user text.
func wrapLogicalText(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	if s == "" {
		return []string{""}
	}
	if rtlDisplayMode() == rtl.Logical || strings.Contains(s, "\x1b") {
		return legacyWrap(s, width)
	}
	lines, err := layoutRenderedText(s, nil, width, rtlDisplayPolicy())
	if err != nil {
		return legacyWrap(s, width)
	}
	return lines
}

func legacyWrap(s string, width int) []string {
	wrapped := ansi.Hardwrap(ansi.Wordwrap(s, width, " \t"), width, false)
	lines := strings.Split(wrapped, "\n")
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// layoutSemanticText is used by the Markdown renderer. Unlike wrapLogicalText
// it never accepts ANSI input. ErrInvalidSpans remains observable to tests;
// production callers use semanticFallback to retain safe logical output.
func layoutSemanticText(logical string, spans []rtl.Span, width int) ([]string, error) {
	if strings.Contains(logical, "\x1b") {
		return nil, errors.New("ui: semantic RTL input contains ANSI")
	}
	if rtlDisplayMode() == rtl.Logical {
		return renderLogicalSemantic(logical, spans, width)
	}
	return layoutRenderedText(logical, spans, width, rtlDisplayPolicy())
}

func renderLogicalSemantic(logical string, spans []rtl.Span, width int) ([]string, error) {
	// Validate using the engine even when visual reordering is disabled.
	if _, err := rtl.Layout(logical, spans, max(width, 1), rtl.Policy{Mode: rtl.Logical, Base: rtl.Auto}); err != nil {
		return nil, err
	}
	if len(spans) == 0 {
		return legacyWrap(logical, width), nil
	}

	// Emit styling before the legacy ANSI-aware wrapper only in logical mode.
	// Visual modes always emit styles after layout.
	var b strings.Builder
	pos := 0
	for _, span := range spans {
		if span.Start > pos {
			b.WriteString(logical[pos:span.Start])
		}
		b.WriteString(renderStyleID(span.StyleID, logical[span.Start:span.End]))
		pos = span.End
	}
	b.WriteString(logical[pos:])
	return legacyWrap(b.String(), width), nil
}

func semanticFallback(logical string, width int) []string {
	return legacyWrap(logical, max(width, 1))
}

// truncateProseToWidth is for human-readable, unstyled prose rows such as
// notices and permission reasons. Opaque tool arguments and source output do
// not use it: those remain LTR/code-owned data unless their caller can provide
// real semantic spans.
func truncateProseToWidth(s string, width int, tail string) string {
	if width <= 0 {
		return ""
	}
	if rtlDisplayMode() == rtl.Logical || strings.Contains(s, "\x1b") {
		return truncateToWidth(s, width, tail)
	}
	lines, err := layoutRenderedText(s, nil, width, rtlDisplayPolicy())
	if err != nil || len(lines) == 0 {
		return truncateToWidth(s, width, tail)
	}
	if len(lines) == 1 {
		return lines[0]
	}
	tailWidth := ansi.StringWidth(tail)
	if tailWidth >= width {
		return ansi.Truncate(tail, width, "")
	}
	return ansi.Truncate(lines[0], width-tailWidth, "") + tail
}
