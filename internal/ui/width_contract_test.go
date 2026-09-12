package ui

// Regression Reproduction Recipe ("Saw it Red — what this guards against"):
// A dependency bump of github.com/mattn/go-runewidth (or reverting the grapheme-cluster
// fix in the local bubbles fork at third_party/bubbles) would cause cursor navigation
// to drift on lines with Arabic combining marks (tashkeel). The local fork replaced
// go-runewidth with graphemeClusters(); this test guards that behavior.
//
// WARNING: go.mod uses a local replace for bubbles (./third_party/bubbles), which has
// its own go.mod and go.sum. Restoring state must cover both modules.
//
// To simulate the regression and confirm the tripwire catches it:
//   1. Revert the grapheme-cluster fix in the local fork, OR bump go-runewidth:
//        go mod edit -require github.com/mattn/go-runewidth@v0.0.29
//        go mod tidy
//   2. Run the tripwire test:
//        go test -v -run TestTripwire_TextareaColumnMappingChanged ./internal/ui
//      => FAILS: "TRIPWIRE: textarea column mapping changed..." (column 1:1 breaks)
//   3. Restore clean repository state (both modules):
//        git checkout -- go.mod go.sum third_party/bubbles/go.mod third_party/bubbles/go.sum

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"nabd/internal/agent"
)

type corpusItem struct {
	category string
	name     string
	text     string
}

func unicodeCorpus() []corpusItem {
	return []corpusItem{
		// Category (a): Zero-Width Joiner (ZWJ) sequences
		{"zwj", "family", "👨‍👩‍👧‍👦"},
		{"zwj", "rainbow_flag", "🏳️‍🌈"},
		{"zwj", "profession", "🧑🏿‍💻"},
		{"zwj", "couple", "👩‍❤️‍💋‍👨"},
		{"zwj", "chef", "👩‍🍳"},
		{"zwj", "multi_sequence", "👨‍👩‍👧‍👦🏳️‍🌈🧑🏿‍💻👩‍🍳"},
		{"zwj", "leading_zwj", "\u200D🍳"},
		{"zwj", "trailing_zwj", "🍳\u200D"},

		// Category (b): Regional Indicators
		{"regional", "us_flag", "🇺🇸"},
		{"regional", "dk_flag", "🇩🇰"},
		{"regional", "jp_flag", "🇯🇵"},
		{"regional", "sa_flag", "🇸🇦"},
		{"regional", "odd_indicator_3", "🇺🇸🇩"},
		{"regional", "odd_indicator_1", "🇺"},
		{"regional", "adjacent_flags", "Flag: 🇺🇸 (USA) and 🇩🇰 (DK)"},

		// Category (c): Arabic diacritics/tashkeel and zero-width combining marks
		{"arabic_combining", "tashkeel_sentence", "اَلْعَرَبِيَّةُ لُغَةٌ سَامِيَّةٌ"},
		{"arabic_combining", "heavy_shadda_kasra", "مُتَعَدِّدُ التَّشْكِيلِ"},
		{"arabic_combining", "quranic_dagger", "بِسْمِ ٱللَّهِ ٱلرَّحْمَٰنِ ٱلرَّحِيمِ"},
		{"arabic_combining", "combining_acute", "e\u0301 vs é"},
		{"arabic_combining", "stacked_zalgo", "Z\u0300\u0301\u0302\u0303\u0304a\u0305\u0306\u0307\u0308l"},
		{"arabic_combining", "isolated_marks", "\u064E\u064F\u0650\u0300\u0301"},

		// Category (d): CJK wide characters
		{"cjk", "japanese", "日本語テキスト・ひらがな・カタカナ・漢字"},
		{"cjk", "chinese", "世界你好，这是一个测试字符串。繁體中文測試。"},
		{"cjk", "korean", "안녕하세요 세계! 한국어 텍스트 테스트"},
		{"cjk", "hangul_jamo", "\u1100\u1161 decomposed"},
		{"cjk", "fullwidth_latin", "ＡＢＣＤＥＦＧ　１２３４５　！？"},
		{"cjk", "mixed_multilingual", "CJK: 日本語, Arabic: العربية, Emoji: 🚀, Latin: Test"},

		// Category (e): Control characters & ANSI escape codes
		{"control_ansi", "whitespace_tabs", "col1\tcol2\tcol3\nline2"},
		{"control_ansi", "c0_controls", "hello\x00world\a\b\x7f"},
		{"control_ansi", "sgr_colors", "\x1b[31mRed Text\x1b[0m and \x1b[1;32mBold Green\x1b[0m"},
		{"control_ansi", "truecolor_styled", "\x1b[38;2;255;100;50mTrueColor RGB\x1b[0m"},
		{"control_ansi", "mixed_ansi_wide", "\x1b[34m[INFO]\x1b[0m \x1b[1mمرحبا\x1b[0m 🚀 \x1b[32m日本語\x1b[0m"},
		{"control_ansi", "unclosed_ansi", "Prefix \x1b[31mUnclosed Red Text that goes on"},
		{"control_ansi", "zero_width_chars", "Soft\u00ADhyphen and zero\u200Bwidth\u200Cjoiner"},
	}
}

// Invariant 1: Sum of segment widths equals total string width.
func TestWidthContract_Invariant_SegmentSumEqualsTotal(t *testing.T) {
	for _, tc := range unicodeCorpus() {
		t.Run(tc.category+"/"+tc.name, func(t *testing.T) {
			// Delimited token partition: when partitioned by standard separator,
			// sum of parts + separators must equal total width.
			tokens := strings.Split(tc.text, " ")
			if len(tokens) > 1 {
				totalW := ansi.StringWidth(tc.text)
				sumW := 0
				for i, tok := range tokens {
					if i > 0 {
						sumW += ansi.StringWidth(" ")
					}
					sumW += ansi.StringWidth(tok)
				}
				if sumW != totalW {
					t.Errorf("[%s/%s] token partition sum mismatch: sum=%d, total=%d: %q",
						tc.category, tc.name, sumW, totalW, tc.text)
				}
			}

			// UI Card Composition Additivity:
			// A row composed of prefix, text, and suffix must equal the sum of its parts.
			prefix := "[INFO] "
			suffix := " (end)"
			composed := prefix + tc.text + suffix
			composedW := ansi.StringWidth(composed)
			expectedW := ansi.StringWidth(prefix) + ansi.StringWidth(tc.text) + ansi.StringWidth(suffix)
			if composedW != expectedW {
				t.Errorf("[%s/%s] composed card width mismatch: got=%d, expected=%d",
					tc.category, tc.name, composedW, expectedW)
			}
		})
	}
}

// Invariant 2: Width remains invariant under splitting and reassembly.
func TestWidthContract_Invariant_SplitAndReassembly(t *testing.T) {
	for _, tc := range unicodeCorpus() {
		t.Run(tc.category+"/"+tc.name, func(t *testing.T) {
			origW := ansi.StringWidth(tc.text)

			// 1. Newline split and reassembly
			nlSplit := strings.Split(tc.text, "\n")
			nlReassembled := strings.Join(nlSplit, "\n")
			if nlReassembled != tc.text {
				t.Errorf("[%s/%s] newline reassembly corrupted content", tc.category, tc.name)
			}
			if w := ansi.StringWidth(nlReassembled); w != origW {
				t.Errorf("[%s/%s] reassembled newline width changed: %d != %d", tc.category, tc.name, w, origW)
			}

			// 2. Space split and reassembly
			spSplit := strings.Split(tc.text, " ")
			spReassembled := strings.Join(spSplit, " ")
			if spReassembled != tc.text {
				t.Errorf("[%s/%s] space reassembly corrupted content", tc.category, tc.name)
			}
			if w := ansi.StringWidth(spReassembled); w != origW {
				t.Errorf("[%s/%s] reassembled space width changed: %d != %d", tc.category, tc.name, w, origW)
			}

			// 3. Wrapping idempotence:
			for _, limit := range []int{10, 20, 40, 80} {
				// a. constrainLines idempotence: each constrained line must be invariant under re-constraining
				lines := constrainLines(tc.text, limit)
				for idx, l := range lines {
					reWrapped := constrainLines(l, limit)
					if len(reWrapped) != 1 {
						t.Errorf("[%s/%s/w=%d] constrainLines line %d not idempotent: split into %d lines",
							tc.category, tc.name, limit, idx, len(reWrapped))
					} else if reWrapped[0] != l {
						t.Errorf("[%s/%s/w=%d] constrainLines line %d changed content on re-wrap",
							tc.category, tc.name, limit, idx)
					}
				}

				// b. visualRowsSplit idempotence: each row produced must occupy exactly 1 visual row
				vRows := visualRowsSplit(tc.text, limit)
				for idx, r := range vRows {
					if rows := visualRowsOf(r, limit); rows != 1 {
						t.Errorf("[%s/%s/w=%d] visualRowsOf row %d is %d, want 1: %q",
							tc.category, tc.name, limit, idx, rows, r)
					}
				}
			}
		})
	}
}

// Invariant 3: Clamping/truncating to width N must never exceed width N.
func TestWidthContract_Invariant_ClampingNeverExceedsBudget(t *testing.T) {
	for _, tc := range unicodeCorpus() {
		t.Run(tc.category+"/"+tc.name, func(t *testing.T) {
			for n := 0; n <= 60; n++ {
				// 1. truncateToWidth with tail
				truncTail := truncateToWidth(tc.text, n, "…")
				if w := ansi.StringWidth(truncTail); w > n {
					t.Errorf("[%s/%s/n=%d] truncateToWidth with tail exceeded budget: width %d > %d: %q",
						tc.category, tc.name, n, w, n, truncTail)
				}
				if !utf8.ValidString(truncTail) {
					t.Errorf("[%s/%s/n=%d] truncateToWidth with tail returned invalid UTF-8",
						tc.category, tc.name, n)
				}

				// 2. truncateToWidth without tail
				truncEmpty := truncateToWidth(tc.text, n, "")
				if w := ansi.StringWidth(truncEmpty); w > n {
					t.Errorf("[%s/%s/n=%d] truncateToWidth without tail exceeded budget: width %d > %d: %q",
						tc.category, tc.name, n, w, n, truncEmpty)
				}
				if !utf8.ValidString(truncEmpty) {
					t.Errorf("[%s/%s/n=%d] truncateToWidth without tail returned invalid UTF-8",
						tc.category, tc.name, n)
				}

				// 3. constrainLines: all resulting lines must be <= n when n >= 2
				// (atomic wide characters like CJK/emoji occupy 2 cells and cannot be divided)
				if n >= 2 {
					lines := constrainLines(tc.text, n)
					for idx, l := range lines {
						if w := ansi.StringWidth(l); w > n {
							t.Errorf("[%s/%s/n=%d] constrainLines line %d width %d > %d: %q",
								tc.category, tc.name, n, idx, w, n, l)
						}
						if !utf8.ValidString(l) {
							t.Errorf("[%s/%s/n=%d] constrainLines line %d invalid UTF-8",
								tc.category, tc.name, n, idx)
						}
					}

					// 4. visualRowsSplit: all resulting rows must be <= n
					vRows := visualRowsSplit(tc.text, n)
					for idx, r := range vRows {
						if w := ansi.StringWidth(r); w > n {
							t.Errorf("[%s/%s/n=%d] visualRowsSplit line %d width %d > %d: %q",
								tc.category, tc.name, n, idx, w, n, r)
						}
						if !utf8.ValidString(r) {
							t.Errorf("[%s/%s/n=%d] visualRowsSplit line %d invalid UTF-8",
								tc.category, tc.name, n, idx)
						}
					}
				}
			}

			// Separator line exactness: separatorLine(n) must equal exactly n
			for _, n := range []int{1, 5, 20, 80, 120} {
				sep := separatorLine(n)
				if w := ansi.StringWidth(sep); w != n {
					t.Errorf("separatorLine(%d) width = %d, want %d", n, w, n)
				}
				asciiSep := asciiSeparatorLine(n)
				if w := ansi.StringWidth(asciiSep); w != n {
					t.Errorf("asciiSeparatorLine(%d) width = %d, want %d", n, w, n)
				}
			}
		})
	}
}

// Invariant 4: Composed UI frame must never exceed window bounds.
func TestWidthContract_Invariant_ComposedFrameWithinBounds(t *testing.T) {
	termSizes := []struct {
		width  int
		height int
	}{
		{20, 12},
		{40, 20},
		{80, 24},
		{120, 40},
	}

	for _, tc := range unicodeCorpus() {
		for _, sz := range termSizes {
			t.Run(tc.name+"/"+strings.TrimSpace(tc.category), func(t *testing.T) {
				f := newFeedAt(t, sz.width, sz.height)

				// Populate feed with complex events containing the test text
				f.Update(agentEventBatchMsg{Events: []agent.Event{
					{Seq: 1, Type: agent.UserMsg, Text: tc.text},
					{Seq: 2, Type: agent.TextDelta, Text: tc.text},
					{Seq: 3, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t1", Name: "tool", Output: tc.text}},
					{Seq: 4, Type: agent.TurnEnd},
				}})

				// Set composer content to test text
				f.composer.setValue(tc.text)

				v := f.View()

				// UTF-8 invariant
				if !utf8.ValidString(v) {
					t.Fatalf("[%s] View() produced invalid UTF-8", tc.name)
				}

				effectiveWidth := max(sz.width, minViewportWidth)
				lines := strings.Split(v, "\n")
				for i, l := range lines {
					if w := ansi.StringWidth(l); w > effectiveWidth {
						t.Errorf("[%s] line %d width %d > %d: %q",
							tc.name, i, w, effectiveWidth, l)
					}
				}

				// Height invariant
				if len(lines) > sz.height && sz.height >= requiredFloor(f) {
					t.Errorf("[%s] lines %d > terminal height %d", tc.name, len(lines), sz.height)
				}
			})
		}
	}
}

// Tripwire: Detects when the underlying column-mapping behavior of the composer
// textarea changes (e.g. a dependency bump to the local bubbles fork or a
// regression in grapheme-cluster handling).
//
// GREEN means "the known-correct behavior is intact: combining marks are zero-width
// and vertical navigation preserves column 1:1 across the ASCII/Arabic boundary."
// RED means the behavior shifted — could be a fix upstream or a regression; either
// way it warrants human review.
//
// LATENT RISK: third_party/bubbles/go.mod still requires mattn/go-runewidth (as of
// this writing, indirectly via go mod tidy). If a future change re-introduces
// rw.RuneWidth in CursorDown/CursorUp, this test catches it — but only if the PR
// reviewer also verifies go.mod hasn't promoted go-runewidth back to direct.
func TestTripwire_TextareaColumnMappingChanged(t *testing.T) {
	c := newComposer()
	text := "0123456789\nاَلْعَرَبِيَّةُ"
	c.setValue(text)

	// Baseline: under the grapheme-cluster-aware local fork (third_party/bubbles),
	// combining marks are zero-width. Column mapping must be identity.
	for targetCol := 1; targetCol <= 5; targetCol++ {
		for c.ta.Line() > 0 {
			c.ta.CursorUp()
		}
		c.ta.SetCursor(targetCol)
		c.ta.CursorDown()
		gotCol := c.ta.LineInfo().ColumnOffset
		t.Logf("targetCol=%d -> gotCol=%d", targetCol, gotCol)
		if gotCol != targetCol {
			t.Errorf("TRIPWIRE: textarea column mapping changed at targetCol=%d: got %d, want %d. "+
				"Review whether the local bubbles fork or go-runewidth behavior shifted.",
				targetCol, gotCol, targetCol)
		}
	}
}

// Correctness invariant: vertical navigation preserves column alignment across
// ASCII and Arabic-combining-mark lines. A zero-width combining mark must NOT
// consume a visible column. This is the same invariant as the tripwire above,
// but stated here as an explicit contract so it can be referenced from
// THREAT_MODEL.md and security review gates.
func TestCorrectness_ComposerNavigationColumnAlignment(t *testing.T) {
	c := newComposer()
	text := "0123456789\nاَلْعَرَبِيَّةُ"
	c.setValue(text)

	for targetCol := 1; targetCol <= 5; targetCol++ {
		for c.ta.Line() > 0 {
			c.ta.CursorUp()
		}
		c.ta.SetCursor(targetCol)
		c.ta.CursorDown()
		gotCol := c.ta.LineInfo().ColumnOffset
		if gotCol != targetCol {
			t.Errorf("correctness: vertical navigation target column %d became %d on Arabic line "+
				"(combining marks must be zero-width)", targetCol, gotCol)
		}
	}
}
