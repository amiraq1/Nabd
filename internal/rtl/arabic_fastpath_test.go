package rtl

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// fastPathTestCase defines a table-driven test case for fast-path boundary testing.
type fastPathTestCase struct {
	name          string
	input         string
	expectedRunes [][]rune // nil if checking generic invariants only
	note          string
}

// buildFastPathTestCases constructs all required cases specified in Step 1.
func buildFastPathTestCases() []fastPathTestCase {
	cases := []fastPathTestCase{
		// 1. 0 marks: contextual shape only
		{
			name:  "0_marks_isolated",
			input: "ب",
			expectedRunes: [][]rune{
				{0xFE8F}, // Beh isolated
			},
			note: "0 marks: isolated form",
		},
		{
			name:  "0_marks_initial_final",
			input: "بت",
			expectedRunes: [][]rune{
				{0xFE91}, // Beh initial
				{0xFE96}, // Teh final
			},
			note: "0 marks: initial and final contextual forms",
		},
		{
			name:  "0_marks_medial",
			input: "تبت",
			expectedRunes: [][]rune{
				{0xFE97}, // Teh initial
				{0xFE92}, // Beh medial
				{0xFE96}, // Teh final
			},
			note: "0 marks: medial contextual form",
		},
		{
			name:  "0_marks_final",
			input: "تب",
			expectedRunes: [][]rune{
				{0xFE97}, // Teh initial
				{0xFE90}, // Beh final
			},
			note: "0 marks: final contextual form",
		},
		{
			name:  "0_marks_unshaped_ascii",
			input: "hello",
			expectedRunes: [][]rune{
				{'h'}, {'e'}, {'l'}, {'l'}, {'o'},
			},
			note: "0 marks: non-Arabic text remains unshaped",
		},

		// 2. 1 mark: single standard diacritic
		{
			name:  "1_mark_isolated_fatha",
			input: "بَ", // Beh + Fatha
			expectedRunes: [][]rune{
				{0xFE8F, 0x064E},
			},
			note: "1 mark: Beh isolated + Fatha",
		},
		{
			name:  "1_mark_word_fatha",
			input: "كَتَبَ", // Kaf+Fatha, Teh+Fatha, Beh+Fatha
			expectedRunes: [][]rune{
				{0xFEDB, 0x064E}, // Kaf initial + Fatha
				{0xFE98, 0x064E}, // Teh medial + Fatha
				{0xFE90, 0x064E}, // Beh final + Fatha
			},
			note: "1 mark: each letter carries 1 transparent mark",
		},
		{
			name:  "1_mark_lam_alef",
			input: "لَا", // Lam + Fatha + Alef
			expectedRunes: [][]rune{
				{0xFEFB, 0x064E}, // Lam-Alef isolated ligature + Fatha
			},
			note: "1 mark: Lam-Alef with intervening Fatha",
		},
		{
			name:  "1_mark_kasra",
			input: "مِ", // Meem + Kasra
			expectedRunes: [][]rune{
				{0xFEE1, 0x0650}, // Meem isolated + Kasra
			},
			note: "1 mark: Meem isolated + Kasra",
		},

		// 3. 2 cached marks: pair from inside table, compared to general path
		{
			name:  "2_cached_marks_shadda_fatha",
			input: "بَّ", // Beh + Shadda (0x0651) + Fatha (0x064E)
			expectedRunes: [][]rune{
				{0xFE8F, 0x0651, 0x064E},
			},
			note: "2 cached marks: Shadda + Fatha pair in table",
		},
		{
			name:  "2_cached_marks_fatha_dagger_alef",
			input: "مَٰ", // Meem + Fatha (0x064E) + Dagger Alef (0x0670)
			expectedRunes: [][]rune{
				{0xFEE1, 0x064E, 0x0670},
			},
			note: "2 cached marks: Fatha + Dagger Alef pair in table",
		},
		{
			name:  "2_cached_marks_lam_alef",
			input: "\u0644\u0651\u064E\u0627", // Lam + Shadda (0x0651) + Fatha (0x064E) + Alef
			expectedRunes: [][]rune{
				{0xFEFB, 0x0651, 0x064E},
			},
			note: "2 cached marks: Lam-Alef with Shadda + Fatha pair",
		},
		{
			name:  "2_cached_marks_kasratan_shadda",
			input: "رٍّ", // Reh + Kasratan (0x064D) + Shadda (0x0651)
			expectedRunes: [][]rune{
				{0xFEAD, 0x064D, 0x0651}, // Reh isolated + Kasratan + Shadda
			},
			note: "2 cached marks: Kasratan + Shadda pair",
		},

		// 4. 3 marks: exits pair path, order preserved
		{
			name:  "3_marks_shadda_fatha_sukun",
			input: "بَّْ", // Beh + Shadda (0x0651) + Fatha (0x064E) + Sukun (0x0652)
			expectedRunes: [][]rune{
				{0xFE8F, 0x0651, 0x064E, 0x0652},
			},
			note: "3 marks: exits pairs table, all 3 marks preserved in literal order",
		},
		{
			name:  "3_marks_lam_alef",
			input: "\u0644\u0651\u064E\u0627\u0652", // Lam + Shadda (0x0651) + Fatha (0x064E) + Alef + Sukun (0x0652)
			expectedRunes: [][]rune{
				{0xFEFB, 0x0651, 0x064E, 0x0652},
			},
			note: "3 marks on Lam-Alef: exits pair path, literal order preserved",
		},

		// 5. mark outside list: U+0653 (Maddah above), U+0654 (Hamza above)
		{
			name:  "mark_outside_list_0653_maddah",
			input: "ب\u0653", // Beh + Maddah Above (U+0653)
			expectedRunes: [][]rune{
				{0xFE8F, 0x0653},
			},
			note: "mark outside list: U+0653 maddah above not dropped and not converted",
		},
		{
			name:  "mark_outside_list_0654_hamza",
			input: "ب\u0654", // Beh + Hamza Above (U+0654)
			expectedRunes: [][]rune{
				{0xFE8F, 0x0654},
			},
			note: "mark outside list: U+0654 hamza above not dropped and not converted",
		},
		{
			name:  "mark_outside_list_lam_alef_0654",
			input: "ل\u0654ا", // Lam + Hamza Above (U+0654) + Alef
			expectedRunes: [][]rune{
				{0xFEFB, 0x0654},
			},
			note: "mark outside list: Lam-Alef with U+0654",
		},

		// 6. 2 different marks in both orders (AB and BA): no canonical sorting
		{
			name:  "marks_order_AB_shadda_fatha",
			input: "ب\u0651\u064E", // Shadda then Fatha
			expectedRunes: [][]rune{
				{0xFE8F, 0x0651, 0x064E},
			},
			note: "2 marks order AB: Shadda then Fatha preserved literally",
		},
		{
			name:  "marks_order_BA_fatha_shadda",
			input: "ب\u064E\u0651", // Fatha then Shadda
			expectedRunes: [][]rune{
				{0xFE8F, 0x064E, 0x0651},
			},
			note: "2 marks order BA: Fatha then Shadda preserved without canonical sorting",
		},
	}

	// 7. 255 / 256 / 257 textRune boundaries (each in 3 forms):
	// Form 1: plain letters
	// Form 2: with marks
	// Form 3: Lam-Alef at the exact boundary
	for _, nRunes := range []int{255, 256, 257} {
		suffix := string(rune('0'+nRunes/100)) + string(rune('0'+(nRunes/10)%10)) + string(rune('0'+nRunes%10))

		// Form 1: plain letters
		var bPlain strings.Builder
		for i := 0; i < nRunes; i++ {
			// Alternate Beh (0628) and Teh (062A)
			if i%2 == 0 {
				bPlain.WriteRune(0x0628)
			} else {
				bPlain.WriteRune(0x062A)
			}
		}
		cases = append(cases, fastPathTestCase{
			name:  "boundary_" + suffix + "_form1_plain",
			input: bPlain.String(),
			note:  "exact " + suffix + " runes: plain alternating Arabic letters",
		})

		// Form 2: with marks
		// Each base letter has 1 mark (2 runes per cluster).
		// If nRunes is odd, last rune is a plain letter without mark.
		var bMarks strings.Builder
		numPairs := nRunes / 2
		hasTrailing := (nRunes % 2) != 0
		for i := 0; i < numPairs; i++ {
			bMarks.WriteRune(0x0628) // Beh
			bMarks.WriteRune(0x064E) // Fatha
		}
		if hasTrailing {
			bMarks.WriteRune(0x0628)
		}
		cases = append(cases, fastPathTestCase{
			name:  "boundary_" + suffix + "_form2_with_marks",
			input: bMarks.String(),
			note:  "exact " + suffix + " runes: letters with transparent marks",
		})

		// Form 3: Lam-Alef at the boundary
		// Place Lam-Alef (2 runes: 0x0644, 0x0627) ending exactly at rune index nRunes.
		var bLamAlef strings.Builder
		for i := 0; i < nRunes-2; i++ {
			bLamAlef.WriteRune(0x062A) // Teh
		}
		bLamAlef.WriteRune(0x0644) // Lam (rune nRunes-2)
		bLamAlef.WriteRune(0x0627) // Alef (rune nRunes-1)
		cases = append(cases, fastPathTestCase{
			name:  "boundary_" + suffix + "_form3_lam_alef_boundary",
			input: bLamAlef.String(),
			note:  "exact " + suffix + " runes: Lam-Alef positioned at boundary",
		})
	}

	return cases
}

// verifySourceRangesAndPartition enforces invariant (c):
// Source ranges cover input text exactly once: contiguous, no gaps, no overlaps, covering [0, len(input)).
func verifySourceRangesAndPartition(t *testing.T, tcName, input string, items []shapedItem) {
	t.Helper()
	if len(input) == 0 {
		if len(items) != 0 {
			t.Fatalf("[%s] empty input produced %d items", tcName, len(items))
		}
		return
	}

	if len(items) == 0 {
		t.Fatalf("[%s] non-empty input produced 0 items", tcName)
	}

	// First item starts at byte 0
	if items[0].c.SrcBytes[0] != 0 {
		t.Fatalf("[%s] first item SrcBytes[0] = %d, want 0", tcName, items[0].c.SrcBytes[0])
	}

	// Last item ends at len(input)
	last := items[len(items)-1]
	if last.c.SrcBytes[1] != len(input) {
		t.Fatalf("[%s] last item SrcBytes[1] = %d, want len(input) %d", tcName, last.c.SrcBytes[1], len(input))
	}

	// Contiguity check: items[i].SrcBytes[0] == items[i-1].SrcBytes[1]
	// Range check: 0 <= SrcBytes[0] < SrcBytes[1] <= len(input)
	var reconstructed strings.Builder
	for i, it := range items {
		start := it.c.SrcBytes[0]
		end := it.c.SrcBytes[1]

		if start < 0 || end > len(input) || start >= end {
			t.Fatalf("[%s] item %d invalid range [%d, %d) for text length %d",
				tcName, i, start, end, len(input))
		}

		if i > 0 {
			prevEnd := items[i-1].c.SrcBytes[1]
			if start != prevEnd {
				t.Fatalf("[%s] item %d discontinuity: prevEnd %d != currStart %d (gap or overlap)",
					tcName, i, prevEnd, start)
			}
		}

		slice := input[start:end]
		if len(slice) == 0 {
			t.Fatalf("[%s] item %d empty source slice", tcName, i)
		}
		reconstructed.WriteString(slice)
	}

	if reconstructed.String() != input {
		t.Fatalf("[%s] source range reconstruction mismatch:\n  want: %q\n   got: %q",
			tcName, input, reconstructed.String())
	}
}

// verifyLiteralMarkOrder enforces invariant (b):
// Transparent marks attached to a base character (or ligature) must appear in the exact order of the input.
func verifyLiteralMarkOrder(t *testing.T, tcName, input string, items []shapedItem) {
	t.Helper()
	for i, it := range items {
		visRunes := []rune(it.c.Text)
		srcText := input[it.c.SrcBytes[0]:it.c.SrcBytes[1]]

		// Filter transparent marks from the source slice
		var srcMarks []rune
		for _, r := range srcText {
			if joiningType(r) == joiningClassTrans {
				srcMarks = append(srcMarks, r)
			}
		}

		if len(srcMarks) == 0 {
			continue
		}

		// In visual output, transparent marks follow the base glyph (at visRunes[1:])
		if len(visRunes) <= 1 {
			t.Fatalf("[%s] item %d has %d source marks %U, but visual runes length is only %d (%U)",
				tcName, i, len(srcMarks), srcMarks, len(visRunes), visRunes)
		}

		visMarks := visRunes[1:]
		if len(visMarks) != len(srcMarks) {
			t.Fatalf("[%s] item %d mark count mismatch: visual marks %U (len %d) vs source marks %U (len %d)",
				tcName, i, visMarks, len(visMarks), srcMarks, len(srcMarks))
		}

		for mi := range srcMarks {
			if visMarks[mi] != srcMarks[mi] {
				t.Fatalf("[%s] item %d mark %d mismatch: got %U, want %U (literal mark order violated)",
					tcName, i, mi, visMarks[mi], srcMarks[mi])
			}
		}
	}
}

// TestArabicFastPathBoundaries runs the table-driven test suite for all Step 1 cases.
// Every case checks:
// (a) Visual output correct
// (b) Mark order literally preserved
// (c) Source ranges cover input exactly once (contiguous, no gaps, no overlaps, covers [0, len(input)))
// (d) No panic
func TestArabicFastPathBoundaries(t *testing.T) {
	cases := buildFastPathTestCases()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// (d) Verify no panic during execution
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("[%s] PANIC encountered: %v", tc.name, r)
				}
			}()

			// Run fast path
			fastItems := shapeArabicRunWithConfig(tc.input, 0, 0, 1, Prose, 0, nil, shapingFastPathConfig{})

			// (c) Verify source ranges and exact partitioning
			verifySourceRangesAndPartition(t, tc.name, tc.input, fastItems)

			// (b) Verify literal mark order
			verifyLiteralMarkOrder(t, tc.name, tc.input, fastItems)

			// (a) Verify visual output correctness if expected runes provided
			if tc.expectedRunes != nil {
				if len(fastItems) != len(tc.expectedRunes) {
					t.Fatalf("[%s] item count want %d, got %d", tc.name, len(tc.expectedRunes), len(fastItems))
				}
				for i, expRunes := range tc.expectedRunes {
					gotRunes := []rune(fastItems[i].c.Text)
					if len(gotRunes) != len(expRunes) {
						t.Fatalf("[%s] item %d rune count want %d (%U), got %d (%U)",
							tc.name, i, len(expRunes), expRunes, len(gotRunes), gotRunes)
					}
					for ri := range expRunes {
						if gotRunes[ri] != expRunes[ri] {
							t.Fatalf("[%s] item %d rune %d want %U, got %U",
								tc.name, i, ri, expRunes[ri], gotRunes[ri])
						}
					}
				}
			}
		})
	}
}

// TestArabicFastPathEquivalence explicitly tests fast == reference equivalence for every case.
// Runs shapeArabicRunWithConfig with default fast-path vs reference config, and compares with ShapeArabic.
func TestArabicFastPathEquivalence(t *testing.T) {
	cases := buildFastPathTestCases()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 1. Run with fast paths enabled (stack buffers, tables, O(n) neighbor tracking)
			fastItems := shapeArabicRunWithConfig(tc.input, 0, 0, 1, Prose, 0, nil, shapingFastPathConfig{})

			// 2. Run with fast paths disabled (reference algorithm)
			refItems := shapeArabicRunWithConfig(tc.input, 0, 0, 1, Prose, 0, nil, shapingFastPathConfig{
				disableFastPaths: true,
			})

			// 3. Run canonical standalone shaping engine
			canonClusters := ShapeArabic(tc.input)

			// Equivalence check 1: fastItems vs refItems
			if len(fastItems) != len(refItems) {
				t.Fatalf("[%s] count mismatch: fast=%d, ref=%d", tc.name, len(fastItems), len(refItems))
			}
			for i := range fastItems {
				f := fastItems[i]
				r := refItems[i]

				if f.c.Text != r.c.Text {
					t.Fatalf("[%s] item %d text mismatch: fast=%q (%U), ref=%q (%U)",
						tc.name, i, f.c.Text, []rune(f.c.Text), r.c.Text, []rune(r.c.Text))
				}
				if f.c.SrcBytes != r.c.SrcBytes {
					t.Fatalf("[%s] item %d SrcBytes mismatch: fast=%v, ref=%v", tc.name, i, f.c.SrcBytes, r.c.SrcBytes)
				}
				if f.c.SrcRunes != r.c.SrcRunes {
					t.Fatalf("[%s] item %d SrcRunes mismatch: fast=%v, ref=%v", tc.name, i, f.c.SrcRunes, r.c.SrcRunes)
				}
				if f.c.Width != r.c.Width {
					t.Fatalf("[%s] item %d Width mismatch: fast=%d, ref=%d", tc.name, i, f.c.Width, r.c.Width)
				}
			}

			// Equivalence check 2: fastItems vs canonClusters (ShapeArabic)
			if len(fastItems) != len(canonClusters) {
				t.Fatalf("[%s] count mismatch: fastItems=%d, canonClusters=%d", tc.name, len(fastItems), len(canonClusters))
			}
			for i := range fastItems {
				f := fastItems[i]
				c := canonClusters[i]
				canonText := string(c.Visual)

				if f.c.Text != canonText {
					t.Fatalf("[%s] item %d text vs ShapeArabic mismatch: fast=%q (%U), canon=%q (%U)",
						tc.name, i, f.c.Text, []rune(f.c.Text), canonText, c.Visual)
				}
				if f.c.SrcBytes[0] != c.SourceStart || f.c.SrcBytes[1] != c.SourceEnd {
					t.Fatalf("[%s] item %d bytes mismatch: fast=[%d, %d), canon=[%d, %d)",
						tc.name, i, f.c.SrcBytes[0], f.c.SrcBytes[1], c.SourceStart, c.SourceEnd)
				}
			}
		})
	}
}

// TestArabicFastPath_InvalidUTF8_Regression validates the explicit contract for invalid UTF-8:
//   - Visual output contains valid U+FFFD for each decoding error
//   - utf8.ValidString(Visual) == true
//   - Source ranges cover [0, 3) exactly once
//   - RestoreFromSource reconstructs "\xff\xfe\x80" literally
//   - fast == reference equivalence holds
func TestArabicFastPath_InvalidUTF8_Regression(t *testing.T) {
	invalidInput := string([]byte{0xff, 0xfe, 0x80})

	// 1. Fast path
	fastItems := shapeArabicRunWithConfig(invalidInput, 0, 0, 1, Prose, 0, nil, shapingFastPathConfig{})

	// 2. Reference path
	refItems := shapeArabicRunWithConfig(invalidInput, 0, 0, 1, Prose, 0, nil, shapingFastPathConfig{
		disableFastPaths: true,
	})

	// 3. ShapeArabic
	canonClusters := ShapeArabic(invalidInput)

	if len(fastItems) != 3 {
		t.Fatalf("want 3 items, got %d", len(fastItems))
	}

	// Contract check 1: Visual contains valid U+FFFD for each decoding error
	for i, it := range fastItems {
		if it.c.Text != string(utf8.RuneError) {
			t.Fatalf("item %d Text want %q (U+FFFD), got %q", i, string(utf8.RuneError), it.c.Text)
		}
		// Contract check 2: utf8.ValidString(Visual) == true
		if !utf8.ValidString(it.c.Text) {
			t.Fatalf("item %d Text is not valid UTF-8: %q", i, it.c.Text)
		}
	}

	// Contract check 3: Source ranges cover [0, 3) exactly once
	verifySourceRangesAndPartition(t, "invalid_utf8_regression", invalidInput, fastItems)
	if fastItems[0].c.SrcBytes != [2]int{0, 1} || fastItems[1].c.SrcBytes != [2]int{1, 2} || fastItems[2].c.SrcBytes != [2]int{2, 3} {
		t.Fatalf("unexpected SrcBytes: %v, %v, %v", fastItems[0].c.SrcBytes, fastItems[1].c.SrcBytes, fastItems[2].c.SrcBytes)
	}

	// Contract check 4: RestoreFromSource restores 0xFF, 0xFE, 0x80 literally
	var restored strings.Builder
	for _, it := range fastItems {
		restored.WriteString(invalidInput[it.c.SrcBytes[0]:it.c.SrcBytes[1]])
	}
	if restored.String() != invalidInput {
		t.Fatalf("RestoreFromSource mismatch: want %q, got %q", invalidInput, restored.String())
	}

	// Equivalence check: fast == ref == ShapeArabic
	for i := range fastItems {
		if fastItems[i].c.Text != refItems[i].c.Text {
			t.Fatalf("item %d fast Text %q != ref Text %q", i, fastItems[i].c.Text, refItems[i].c.Text)
		}
		if fastItems[i].c.Text != string(canonClusters[i].Visual) {
			t.Fatalf("item %d fast Text %q != canon Visual %q", i, fastItems[i].c.Text, string(canonClusters[i].Visual))
		}
	}
}

// FuzzArabicFastPathEquivalence fuzzes the Arabic shaping engine verifying the invariant:
// fast == reference for any arbitrary text sequence.
func FuzzArabicFastPathEquivalence(f *testing.F) {
	// Seed with all cases from buildFastPathTestCases
	cases := buildFastPathTestCases()
	for _, tc := range cases {
		f.Add(tc.input)
	}

	// Add mandatory static seed for invalid UTF-8 contract
	f.Add(string([]byte{0xff, 0xfe, 0x80}))

	// Add additional diverse seeds
	extraSeeds := []string{
		"",
		"ا",
		"ل",
		"لا",
		"لآ",
		"لأ",
		"لإ",
		"لَا",
		"لَآّ",
		"ب\u200D",
		"ب\u200Cب",
		"مرحبا بالعالم",
		"بِسْمِ اللَّهِ الرَّحْمَٰنِ الرَّحِيمِ",
		"كَتَبَ يُكْتَبُ مَكْتُوبٌ",
		"123 ٤٥٦ 789",
		"/path/to/ملف.txt",
		"خط أول\nخط ثان\r\nخط ثالث",
		string([]byte{0xD8, 0xA8, 0xD9, 0x8E}), // Valid UTF-8 Beh + Fatha
	}
	for _, s := range extraSeeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, text string) {
		// (d) No panic
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC on input %q: %v", text, r)
			}
		}()

		// Fast path
		fastItems := shapeArabicRunWithConfig(text, 0, 0, 1, Prose, 0, nil, shapingFastPathConfig{})

		// Reference path
		refItems := shapeArabicRunWithConfig(text, 0, 0, 1, Prose, 0, nil, shapingFastPathConfig{
			disableFastPaths: true,
		})

		// Equivalence: fast == reference
		if len(fastItems) != len(refItems) {
			t.Fatalf("fuzz length mismatch on input %q: fast=%d, ref=%d", text, len(fastItems), len(refItems))
		}

		for i := range fastItems {
			f := fastItems[i]
			r := refItems[i]

			if f.c.Text != r.c.Text {
				t.Fatalf("fuzz item %d text mismatch on input %q:\n  fast: %q (%U)\n   ref: %q (%U)",
					i, text, f.c.Text, []rune(f.c.Text), r.c.Text, []rune(r.c.Text))
			}
			if f.c.SrcBytes != r.c.SrcBytes {
				t.Fatalf("fuzz item %d SrcBytes mismatch on input %q: fast=%v, ref=%v",
					i, text, f.c.SrcBytes, r.c.SrcBytes)
			}
			if f.c.SrcRunes != r.c.SrcRunes {
				t.Fatalf("fuzz item %d SrcRunes mismatch on input %q: fast=%v, ref=%v",
					i, text, f.c.SrcRunes, r.c.SrcRunes)
			}
		}

		// Partition and source range checks
		if len(text) > 0 && len(fastItems) > 0 {
			if fastItems[0].c.SrcBytes[0] != 0 {
				t.Fatalf("first item start != 0: %d", fastItems[0].c.SrcBytes[0])
			}
			if fastItems[len(fastItems)-1].c.SrcBytes[1] != len(text) {
				t.Fatalf("last item end != len(text): %d != %d",
					fastItems[len(fastItems)-1].c.SrcBytes[1], len(text))
			}
			for i := 1; i < len(fastItems); i++ {
				if fastItems[i-1].c.SrcBytes[1] != fastItems[i].c.SrcBytes[0] {
					t.Fatalf("discontinuity at %d: %d != %d",
						i, fastItems[i-1].c.SrcBytes[1], fastItems[i].c.SrcBytes[0])
				}
			}
		}

		// Valid UTF-8 in visual items
		for i, it := range fastItems {
			if !utf8.ValidString(it.c.Text) {
				t.Fatalf("item %d produced invalid UTF-8 string: %q", i, it.c.Text)
			}
		}
	})
}
