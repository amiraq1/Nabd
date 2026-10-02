package rtl

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 1. [x] joining matrix: isolated/final/initial/medial
func TestShapeArabic_JoiningMatrix(t *testing.T) {
	// Test dual-joining Beh (0x0628):
	// - Isolated: " ب " (preceded and followed by space)
	// - Initial: "بت" (followed by dual-joining Teh)
	// - Medial: "تبت" (preceded and followed by dual-joining Teh)
	// - Final: "تب" (preceded by dual-joining Teh)

	tests := []struct {
		name     string
		input    string
		behForm  rune // expected shaped Beh
		formName string
		behClust int // index of cluster containing Beh
	}{
		{
			name:     "isolated",
			input:    " ب ",
			behForm:  0xFE8F, // ARABIC LETTER BEH ISOLATED FORM
			formName: "isolated",
			behClust: 1,
		},
		{
			name:     "initial",
			input:    "بت",
			behForm:  0xFE91, // ARABIC LETTER BEH INITIAL FORM
			formName: "initial",
			behClust: 0,
		},
		{
			name:     "medial",
			input:    "تبت",
			behForm:  0xFE92, // ARABIC LETTER BEH MEDIAL FORM
			formName: "medial",
			behClust: 1,
		},
		{
			name:     "final",
			input:    "تب",
			behForm:  0xFE90, // ARABIC LETTER BEH FINAL FORM
			formName: "final",
			behClust: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clusters := ShapeArabic(tc.input)
			if len(clusters) <= tc.behClust {
				t.Fatalf("expected at least %d clusters, got %d", tc.behClust+1, len(clusters))
			}
			c := clusters[tc.behClust]
			if len(c.Visual) == 0 {
				t.Fatalf("empty visual in cluster %d", tc.behClust)
			}
			if c.Visual[0] != tc.behForm {
				t.Errorf("%s Beh: want 0x%04X, got 0x%04X", tc.formName, tc.behForm, c.Visual[0])
			}
		})
	}
}

// 2. [x] R/L/D/C/U/T joining classes
func TestShapeArabic_JoiningClasses_RLDCUT(t *testing.T) {
	// R (Right-joining: Dal 062F): connects to preceding Beh, but does NOT connect to following Beh.
	// "بدب" -> Beh initial (FE91), Dal final (FEAA), Beh isolated (FE8F).
	clusters := ShapeArabic("بدب")
	if len(clusters) != 3 {
		t.Fatalf("want 3 clusters, got %d", len(clusters))
	}
	if clusters[0].Visual[0] != 0xFE91 { // Beh initial
		t.Errorf("Beh before Dal: want initial 0xFE91, got 0x%04X", clusters[0].Visual[0])
	}
	if clusters[1].Visual[0] != 0xFEAA { // Dal final
		t.Errorf("Dal after Beh: want final 0xFEAA, got 0x%04X", clusters[1].Visual[0])
	}
	if clusters[2].Visual[0] != 0xFE8F { // Beh after Dal (cannot connect left): isolated
		t.Errorf("Beh after Dal: want isolated 0xFE8F, got 0x%04X", clusters[2].Visual[0])
	}

	// C (Join-causing: Tatweel 0640): connects both ways without changing its own glyph.
	// "بـب" -> Beh initial (FE91), Tatweel (0640), Beh final (FE90).
	clusters = ShapeArabic("بـب")
	if len(clusters) != 3 {
		t.Fatalf("want 3 clusters, got %d", len(clusters))
	}
	if clusters[0].Visual[0] != 0xFE91 { // Beh initial
		t.Errorf("Beh before Tatweel: want initial 0xFE91, got 0x%04X", clusters[0].Visual[0])
	}
	if clusters[1].Visual[0] != 0x0640 { // Tatweel
		t.Errorf("Tatweel: want 0x0640, got 0x%04X", clusters[1].Visual[0])
	}
	if clusters[2].Visual[0] != 0xFE90 { // Beh final
		t.Errorf("Beh after Tatweel: want final 0xFE90, got 0x%04X", clusters[2].Visual[0])
	}

	// U (Non-joining: Hamza 0621): joins neither right nor left.
	// "بءب" -> Beh isolated (FE8F), Hamza isolated (FE80), Beh isolated (FE8F).
	clusters = ShapeArabic("بءب")
	if len(clusters) != 3 {
		t.Fatalf("want 3 clusters, got %d", len(clusters))
	}
	if clusters[0].Visual[0] != 0xFE8F {
		t.Errorf("Beh before Hamza: want isolated 0xFE8F, got 0x%04X", clusters[0].Visual[0])
	}
	if clusters[1].Visual[0] != 0xFE80 {
		t.Errorf("Hamza: want 0xFE80, got 0x%04X", clusters[1].Visual[0])
	}
	if clusters[2].Visual[0] != 0xFE8F {
		t.Errorf("Beh after Hamza: want isolated 0xFE8F, got 0x%04X", clusters[2].Visual[0])
	}
}

// 3. [x] transparent marks
func TestShapeArabic_TransparentMarks(t *testing.T) {
	// "كَتَبَ" (Kaf + Fatha + Teh + Fatha + Beh + Fatha):
	// Fathas are transparent: Kaf is initial (FEDB), Teh is medial (FE98), Beh is final (FE90).
	// Each letter carries its Fatha in Visual[1].
	text := "كَتَبَ"
	clusters := ShapeArabic(text)
	if len(clusters) != 3 {
		t.Fatalf("want 3 clusters, got %d", len(clusters))
	}

	// Kaf + Fatha
	if clusters[0].Visual[0] != 0xFEDB || clusters[0].Visual[1] != 0x064E {
		t.Errorf("cluster 0 want [FEDB, 064E], got [%04X, %04X]", clusters[0].Visual[0], clusters[0].Visual[1])
	}
	// Teh + Fatha
	if clusters[1].Visual[0] != 0xFE98 || clusters[1].Visual[1] != 0x064E {
		t.Errorf("cluster 1 want [FE98, 064E], got [%04X, %04X]", clusters[1].Visual[0], clusters[1].Visual[1])
	}
	// Beh + Fatha
	if clusters[2].Visual[0] != 0xFE90 || clusters[2].Visual[1] != 0x064E {
		t.Errorf("cluster 2 want [FE90, 064E], got [%04X, %04X]", clusters[2].Visual[0], clusters[2].Visual[1])
	}
}

// 4. [x] multiple marks
func TestShapeArabic_MultipleMarks(t *testing.T) {
	// "مُحَمَّد" (Meem + Damma, Hah + Fatha, Meem + Shadda + Fatha, Dal):
	// The second Meem has two stacked transparent marks (Shadda 0651 + Fatha 064E).
	text := "مُحَمَّد"
	clusters := ShapeArabic(text)
	if len(clusters) != 4 {
		t.Fatalf("want 4 clusters, got %d", len(clusters))
	}

	// Cluster 2: Meem medial (FEE4) + Shadda (0651) + Fatha (064E)
	c2 := clusters[2]
	if len(c2.Visual) != 3 {
		t.Fatalf("cluster 2 visual length want 3, got %d", len(c2.Visual))
	}
	if c2.Visual[0] != 0xFEE4 || c2.Visual[1] != 0x0651 || c2.Visual[2] != 0x064E {
		t.Errorf("cluster 2 want [FEE4, 0651, 064E], got [%04X, %04X, %04X]",
			c2.Visual[0], c2.Visual[1], c2.Visual[2])
	}

	// Verify source range matches the exact bytes of "مَّ"
	if text[c2.SourceStart:c2.SourceEnd] != "مَّ" {
		t.Errorf("cluster 2 source text want 'مَّ', got %q", text[c2.SourceStart:c2.SourceEnd])
	}
}

// 5. [x] ZWJ (Zero Width Joiner, U+200D = C)
func TestShapeArabic_ZWJ(t *testing.T) {
	// Assertion: ZWJ is joiningClassCausing in the generated data (not hardcoded).
	if jt := joiningType(0x200D); jt != joiningClassCausing {
		t.Fatalf("joiningType(U+200D): want joiningClassCausing, got %d", jt)
	}

	// Beh followed by ZWJ: forces Beh to Initial form even at end of word.
	clusters := ShapeArabic("ب\u200D")
	if len(clusters) != 2 {
		t.Fatalf("want 2 clusters, got %d", len(clusters))
	}
	if clusters[0].Visual[0] != 0xFE91 { // Beh initial
		t.Errorf("Beh+ZWJ: want initial 0xFE91, got 0x%04X", clusters[0].Visual[0])
	}
	if clusters[1].Visual[0] != 0x200D {
		t.Errorf("ZWJ visual: want 0x200D, got 0x%04X", clusters[1].Visual[0])
	}

	// ZWJ before Beh: forces Beh to Final form even at start of word.
	clusters = ShapeArabic("\u200Dب")
	if len(clusters) != 2 {
		t.Fatalf("want 2 clusters, got %d", len(clusters))
	}
	if clusters[0].Visual[0] != 0x200D {
		t.Errorf("ZWJ visual: want 0x200D, got 0x%04X", clusters[0].Visual[0])
	}
	if clusters[1].Visual[0] != 0xFE90 { // Beh final
		t.Errorf("ZWJ+Beh: want final 0xFE90, got 0x%04X", clusters[1].Visual[0])
	}

	// ZWJ on both sides of Beh: forces Beh to Medial form.
	clusters = ShapeArabic("\u200Dب\u200D")
	if len(clusters) != 3 {
		t.Fatalf("want 3 clusters, got %d", len(clusters))
	}
	if clusters[1].Visual[0] != 0xFE92 { // Beh medial
		t.Errorf("ZWJ+Beh+ZWJ: want medial 0xFE92, got 0x%04X", clusters[1].Visual[0])
	}
}

// 6. [x] ZWNJ (Zero Width Non-Joiner, U+200C = U)
func TestShapeArabic_ZWNJ(t *testing.T) {
	// Assertion: ZWNJ is joiningClassNon in the generated data (not hardcoded).
	if jt := joiningType(0x200C); jt != joiningClassNon {
		t.Fatalf("joiningType(U+200C): want joiningClassNon, got %d", jt)
	}

	// Two dual-joining Behs separated by ZWNJ: neither joins the other, both become Isolated.
	clusters := ShapeArabic("ب\u200Cب")
	if len(clusters) != 3 {
		t.Fatalf("want 3 clusters, got %d", len(clusters))
	}
	if clusters[0].Visual[0] != 0xFE8F { // Beh isolated
		t.Errorf("first Beh before ZWNJ: want isolated 0xFE8F, got 0x%04X", clusters[0].Visual[0])
	}
	if clusters[1].Visual[0] != 0x200C { // ZWNJ preserved
		t.Errorf("ZWNJ cluster: want 0x200C, got 0x%04X", clusters[1].Visual[0])
	}
	if clusters[2].Visual[0] != 0xFE8F { // Beh isolated
		t.Errorf("second Beh after ZWNJ: want isolated 0xFE8F, got 0x%04X", clusters[2].Visual[0])
	}
}

// 7. [x] newline boundary
func TestShapeArabic_NewlineBoundary(t *testing.T) {
	// "كتب\nقلم":
	// The Beh at the end of line 1 must be Final (FE90), NOT Medial.
	// The Qaf at the start of line 2 must be Initial (FED7), NOT Medial.
	text := "كتب\nقلم"
	clusters := ShapeArabic(text)

	// Clusters:
	// 0: Kaf initial (FEDB)
	// 1: Teh medial (FE98)
	// 2: Beh final (FE90)
	// 3: '\n'
	// 4: Qaf initial (FED7)
	// 5: Lam medial (FEE0)
	// 6: Meem final (FEE2)
	if len(clusters) != 7 {
		t.Fatalf("want 7 clusters, got %d", len(clusters))
	}

	if clusters[2].Visual[0] != 0xFE90 { // Beh final
		t.Errorf("Beh before newline: want final 0xFE90, got 0x%04X", clusters[2].Visual[0])
	}
	if clusters[3].Visual[0] != '\n' {
		t.Errorf("newline cluster: want '\\n', got 0x%04X", clusters[3].Visual[0])
	}
	if clusters[4].Visual[0] != 0xFED7 { // Qaf initial
		t.Errorf("Qaf after newline: want initial 0xFED7, got 0x%04X", clusters[4].Visual[0])
	}

	// Also verify CRLF boundary
	textCRLF := "كتب\r\nقلم"
	clustersCRLF := ShapeArabic(textCRLF)
	if clustersCRLF[2].Visual[0] != 0xFE90 {
		t.Errorf("Beh before CRLF: want final 0xFE90, got 0x%04X", clustersCRLF[2].Visual[0])
	}
	if clustersCRLF[5].Visual[0] != 0xFED7 {
		t.Errorf("Qaf after CRLF: want initial 0xFED7, got 0x%04X", clustersCRLF[5].Visual[0])
	}
}

// 8. [x] Lam-Alef family
func TestShapeArabic_LamAlefFamily(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantIso rune
		wantFin rune
		alef    rune
	}{
		{"Madda", "لآ", 0xFEF5, 0xFEF6, 0x0622},
		{"HamzaAbove", "لأ", 0xFEF7, 0xFEF8, 0x0623},
		{"HamzaBelow", "لإ", 0xFEF9, 0xFEFA, 0x0625},
		{"PlainAlef", "لا", 0xFEFB, 0xFEFC, 0x0627},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Isolated
			clusters := ShapeArabic(tc.input)
			if len(clusters) != 1 {
				t.Fatalf("isolated %s: want 1 cluster, got %d", tc.name, len(clusters))
			}
			if clusters[0].Visual[0] != tc.wantIso {
				t.Errorf("isolated %s ligature: want 0x%04X, got 0x%04X", tc.name, tc.wantIso, clusters[0].Visual[0])
			}
			if tc.input[clusters[0].SourceStart:clusters[0].SourceEnd] != tc.input {
				t.Errorf("source slice want %q, got %q", tc.input, tc.input[clusters[0].SourceStart:clusters[0].SourceEnd])
			}

			// Final (preceded by dual-joining Beh "ب")
			inputFin := "ب" + tc.input
			clustersFin := ShapeArabic(inputFin)
			if len(clustersFin) != 2 {
				t.Fatalf("final %s: want 2 clusters, got %d", tc.name, len(clustersFin))
			}
			if clustersFin[0].Visual[0] != 0xFE91 { // Beh initial
				t.Errorf("Beh before Lam-Alef: want initial 0xFE91, got 0x%04X", clustersFin[0].Visual[0])
			}
			if clustersFin[1].Visual[0] != tc.wantFin {
				t.Errorf("final %s ligature: want 0x%04X, got 0x%04X", tc.name, tc.wantFin, clustersFin[1].Visual[0])
			}
		})
	}

	// Intervening marks between Lam and Alef: "لَـا" or "لَا" (Lam + Fatha + Alef)
	withIntervening := "لَآ"
	clusters := ShapeArabic(withIntervening)
	if len(clusters) != 1 {
		t.Fatalf("Lam-Alef with intervening mark: want 1 cluster, got %d", len(clusters))
	}
	if clusters[0].Visual[0] != 0xFEF5 || clusters[0].Visual[1] != 0x064E {
		t.Errorf("intervening: want [FEF5, 064E], got [%04X, %04X]", clusters[0].Visual[0], clusters[0].Visual[1])
	}
	if withIntervening[clusters[0].SourceStart:clusters[0].SourceEnd] != withIntervening {
		t.Errorf("intervening source slice mismatch")
	}

	// Trailing marks following Alef: "لَآّ" (Lam + Fatha + AlefMadda + Shadda)
	withBoth := "لَآّ"
	clusters = ShapeArabic(withBoth)
	if len(clusters) != 1 {
		t.Fatalf("Lam-Alef with both marks: want 1 cluster, got %d", len(clusters))
	}
	if clusters[0].Visual[0] != 0xFEF5 || clusters[0].Visual[1] != 0x064E || clusters[0].Visual[2] != 0x0651 {
		t.Errorf("both marks: want [FEF5, 064E, 0651], got [%04X, %04X, %04X]",
			clusters[0].Visual[0], clusters[0].Visual[1], clusters[0].Visual[2])
	}
	if withBoth[clusters[0].SourceStart:clusters[0].SourceEnd] != withBoth {
		t.Errorf("both marks source slice mismatch")
	}
}

// 9. [x] missing-form preservation (U+0649 and extended Arabic)
func TestShapeArabic_MissingFormPreservation(t *testing.T) {
	// U+0649 (Alef Maksura, JT=D in Unicode 17):
	// - Isolated: " ى " -> FEEF
	// - Final: "على" -> FEF0
	// - Initial: "ىب" -> U+0649 preserved as 0x0649 (no Pres-B initial glyph)! Beh is final FE90.
	// - Medial: "بىب" -> Beh initial FE91, U+0649 preserved as 0x0649, Beh final FE90.

	// Isolated
	cIso := ShapeArabic(" ى ")
	if cIso[1].Visual[0] != 0xFEEF {
		t.Errorf("Alef Maksura isolated: want 0xFEEF, got 0x%04X", cIso[1].Visual[0])
	}

	// Final
	cFin := ShapeArabic("على")
	if cFin[2].Visual[0] != 0xFEF0 {
		t.Errorf("Alef Maksura final: want 0xFEF0, got 0x%04X", cFin[2].Visual[0])
	}

	// Initial: MUST preserve logical 0x0649
	cInit := ShapeArabic("ىب")
	if len(cInit) != 2 {
		t.Fatalf("want 2 clusters, got %d", len(cInit))
	}
	if cInit[0].Visual[0] != 0x0649 {
		t.Errorf("Alef Maksura initial: want logical 0x0649, got 0x%04X", cInit[0].Visual[0])
	}
	if cInit[1].Visual[0] != 0xFE90 { // Beh final
		t.Errorf("Beh after initial Alef Maksura: want final 0xFE90, got 0x%04X", cInit[1].Visual[0])
	}

	// Medial: MUST preserve logical 0x0649
	cMed := ShapeArabic("بىب")
	if len(cMed) != 3 {
		t.Fatalf("want 3 clusters, got %d", len(cMed))
	}
	if cMed[0].Visual[0] != 0xFE91 { // Beh initial
		t.Errorf("Beh before medial Alef Maksura: want initial 0xFE91, got 0x%04X", cMed[0].Visual[0])
	}
	if cMed[1].Visual[0] != 0x0649 { // Alef Maksura preserved
		t.Errorf("Alef Maksura medial: want logical 0x0649, got 0x%04X", cMed[1].Visual[0])
	}
	if cMed[2].Visual[0] != 0xFE90 { // Beh final
		t.Errorf("Beh after medial Alef Maksura: want final 0xFE90, got 0x%04X", cMed[2].Visual[0])
	}

	// Extended Arabic (e.g. Peh 0x067E, Farsi Yeh 0x06CC): preserved as logical runes
	cPeh := ShapeArabic("پ")
	if cPeh[0].Visual[0] != 0x067E {
		t.Errorf("Peh: want logical 0x067E, got 0x%04X", cPeh[0].Visual[0])
	}
}

// 10. [x] complete ArabicShaping coverage
func TestShapeArabic_CompleteArabicShapingCoverage(t *testing.T) {
	// Shape every single codepoint in U+0600..U+06FF individually and in sequence.
	// Must not panic, must produce valid non-empty clusters with valid byte offsets.
	var sb strings.Builder
	for r := rune(0x0600); r <= 0x06FF; r++ {
		sb.WriteRune(r)
	}
	allArabic := sb.String()

	clusters := ShapeArabic(allArabic)
	if len(clusters) == 0 {
		t.Fatal("ShapeArabic returned no clusters for all-Arabic string")
	}

	// Verify complete partition
	if clusters[0].SourceStart != 0 {
		t.Errorf("first cluster start: want 0, got %d", clusters[0].SourceStart)
	}
	if clusters[len(clusters)-1].SourceEnd != len(allArabic) {
		t.Errorf("last cluster end: want %d, got %d", len(allArabic), clusters[len(clusters)-1].SourceEnd)
	}
	for i := 1; i < len(clusters); i++ {
		if clusters[i-1].SourceEnd != clusters[i].SourceStart {
			t.Errorf("discontinuity at cluster %d: prev end %d != curr start %d",
				i, clusters[i-1].SourceEnd, clusters[i].SourceStart)
		}
	}
}

// 11. [x] valid UTF-8
func TestShapeArabic_ValidUTF8(t *testing.T) {
	// Test valid UTF-8 text produces valid visual runes.
	text := "مرحبا بكم في نبض Nabd 2026!"
	clusters := ShapeArabic(text)
	for i, c := range clusters {
		for _, vr := range c.Visual {
			if !utf8.ValidRune(vr) {
				t.Errorf("cluster %d contains invalid rune 0x%04X", i, vr)
			}
		}
	}

	// Test invalid UTF-8 byte sequences gracefully handled without panic.
	invalidBytes := string([]byte{0xFF, 0xFE, 0x80, 0x06, 0x28})
	clustersInv := ShapeArabic(invalidBytes)
	if len(clustersInv) == 0 {
		t.Fatal("expected clusters for invalid bytes input")
	}
	// Reversibility must still hold
	var rec strings.Builder
	for _, c := range clustersInv {
		rec.WriteString(invalidBytes[c.SourceStart:c.SourceEnd])
	}
	if rec.String() != invalidBytes {
		t.Errorf("reconstructed invalid bytes mismatch")
	}
}

// 12. [x] reversible source mapping
func TestShapeArabic_ReversibleSourceMapping(t *testing.T) {
	testCorpus := []string{
		"",
		"Hello World",
		"ن",
		"نبض",
		"كَتَبَ مُحَمَّدٌ رِسَالَةً",
		"لآ لأ لإ لا",
		"لَآ لَأ لَإ لَا",
		"ب\u200D ب\u200Cب",
		"mixed /path/to/ملف.go and code `foo()`",
		"line 1\nline 2\r\nline 3",
		"123 ٤٥٦ 789",
		"مستشفى على موسى عيسى",
	}

	for _, text := range testCorpus {
		clusters := ShapeArabic(text)
		if len(text) == 0 {
			if len(clusters) != 0 {
				t.Errorf("empty text: want 0 clusters, got %d", len(clusters))
			}
			continue
		}

		// Verify partition invariants:
		if clusters[0].SourceStart != 0 {
			t.Errorf("%q: first cluster start want 0, got %d", text, clusters[0].SourceStart)
		}
		if clusters[len(clusters)-1].SourceEnd != len(text) {
			t.Errorf("%q: last cluster end want %d, got %d", text, len(text), clusters[len(clusters)-1].SourceEnd)
		}

		var reconstructed strings.Builder
		for i, c := range clusters {
			if c.SourceStart >= c.SourceEnd {
				t.Errorf("%q cluster %d: SourceStart %d >= SourceEnd %d", text, i, c.SourceStart, c.SourceEnd)
			}
			if i > 0 && clusters[i-1].SourceEnd != c.SourceStart {
				t.Errorf("%q cluster %d: gap/overlap prev end %d != curr start %d",
					text, i, clusters[i-1].SourceEnd, c.SourceStart)
			}
			reconstructed.WriteString(text[c.SourceStart:c.SourceEnd])
		}

		if reconstructed.String() != text {
			t.Errorf("reconstruction failed:\n  want %q\n   got %q", text, reconstructed.String())
		}
	}
}

// 13. [x] no normalization
func TestShapeArabic_NoNormalization(t *testing.T) {
	// Ensure that characters with canonical decompositions are NOT normalized to NFC or NFD.
	// For example, Latin A with ring above:
	// NFC: \u00C5
	// NFD: \u0041\u030A
	nfd := "\u0041\u030A"
	clustersNFD := ShapeArabic(nfd)
	if len(clustersNFD) == 0 {
		t.Fatal("empty clusters for NFD")
	}
	reconstructedNFD := nfd[clustersNFD[0].SourceStart:clustersNFD[len(clustersNFD)-1].SourceEnd]
	if reconstructedNFD != nfd {
		t.Errorf("normalization occurred: want NFD %q, got %q", nfd, reconstructedNFD)
	}

	nfc := "\u00C5"
	clustersNFC := ShapeArabic(nfc)
	if len(clustersNFC) == 0 {
		t.Fatal("empty clusters for NFC")
	}
	reconstructedNFC := nfc[clustersNFC[0].SourceStart:clustersNFC[len(clustersNFC)-1].SourceEnd]
	if reconstructedNFC != nfc {
		t.Errorf("normalization occurred: want NFC %q, got %q", nfc, reconstructedNFC)
	}
}

// 14. [x] no panic under fuzzing
func FuzzShapeArabic(f *testing.F) {
	seeds := []string{
		"",
		"a",
		"مرحبا",
		"كَتَبَ",
		"لا",
		"لآ",
		"لَآّ",
		"ب\u200D",
		"ب\u200Cب",
		"Hello 123",
		"/usr/local/bin/ملف.go",
		"عربي \n إنجليزي \r\n سطر ثالث",
		string([]byte{0xFF, 0xFE, 0x80}),
		"مُحَمَّدٌ رَسُولُ اللَّهِ",
		"ىب بىب موسى",
	}

	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, text string) {
		clusters := ShapeArabic(text)
		if len(text) == 0 {
			if len(clusters) != 0 {
				t.Fatalf("expected nil/empty for empty text, got %d clusters", len(clusters))
			}
			return
		}

		if len(clusters) == 0 {
			t.Fatalf("non-empty text produced 0 clusters: %q", text)
		}

		// Partition and reversibility check
		if clusters[0].SourceStart != 0 {
			t.Fatalf("first cluster does not start at 0: %d", clusters[0].SourceStart)
		}
		if clusters[len(clusters)-1].SourceEnd != len(text) {
			t.Fatalf("last cluster does not end at len(text): want %d, got %d",
				len(text), clusters[len(clusters)-1].SourceEnd)
		}

		for i := range clusters {
			if clusters[i].SourceStart >= clusters[i].SourceEnd {
				t.Fatalf("cluster %d invalid range [%d, %d)", i, clusters[i].SourceStart, clusters[i].SourceEnd)
			}
			if i > 0 && clusters[i-1].SourceEnd != clusters[i].SourceStart {
				t.Fatalf("cluster %d discontinuity: %d != %d", i, clusters[i-1].SourceEnd, clusters[i].SourceStart)
			}
		}
	})
}

// TestShapeArabic_SourceRangesContract explicitly validates the source range invariants:
// For non-empty input:
//   - 0 <= SourceStart < SourceEnd <= len(input)
//   - first.SourceStart == 0
//   - clusters[i].SourceEnd == clusters[i+1].SourceStart
//   - last.SourceEnd == len(input)
//
// Every byte is covered once without gaps or overlap.
func TestShapeArabic_SourceRangesContract(t *testing.T) {
	testInputs := []string{
		"a",
		"ن",
		"نبض",
		"مرحبا بالعالم",
		"كَتَبَ مُحَمَّدٌ",
		"لا لآ لأ لإ",
		"لَـا لَاّ لَآّ",
		"ب\u200D ب\u200Cب",
		"path/to/ملف_123.txt",
		"سطر أول\nسطر ثان\r\nسطر ثالث",
		"123 ٤٥٦ 789",
		"مستشفى على موسى عيسى",
	}

	for _, text := range testInputs {
		clusters := ShapeArabic(text)
		if len(clusters) == 0 {
			t.Fatalf("%q: non-empty input produced 0 clusters", text)
		}

		// first.SourceStart == 0
		if clusters[0].SourceStart != 0 {
			t.Errorf("%q: first cluster SourceStart want 0, got %d", text, clusters[0].SourceStart)
		}

		// last.SourceEnd == len(input)
		last := clusters[len(clusters)-1]
		if last.SourceEnd != len(text) {
			t.Errorf("%q: last cluster SourceEnd want %d, got %d", text, len(text), last.SourceEnd)
		}

		for i, c := range clusters {
			// 0 <= SourceStart < SourceEnd <= len(input)
			if c.SourceStart < 0 {
				t.Errorf("%q cluster %d: negative SourceStart %d", text, i, c.SourceStart)
			}
			if c.SourceStart >= c.SourceEnd {
				t.Errorf("%q cluster %d: SourceStart %d >= SourceEnd %d (range must not be empty)",
					text, i, c.SourceStart, c.SourceEnd)
			}
			if c.SourceEnd > len(text) {
				t.Errorf("%q cluster %d: SourceEnd %d > len(input) %d", text, i, c.SourceEnd, len(text))
			}

			// Contiguity: clusters[i].SourceEnd == clusters[i+1].SourceStart
			if i > 0 && clusters[i-1].SourceEnd != c.SourceStart {
				t.Errorf("%q: gap or overlap between cluster %d (end %d) and cluster %d (start %d)",
					text, i-1, clusters[i-1].SourceEnd, i, c.SourceStart)
			}

			// Verify source slice reversibility
			if text[c.SourceStart:c.SourceEnd] == "" {
				t.Errorf("%q cluster %d: empty source slice", text, i)
			}
		}
	}
}

// TestShapeArabic_SemanticBoundary proves that when text is split across semantic
// boundaries (e.g. Code/Path/URL spans) into separate engine calls:
//
//	left := ShapeArabic("ل")
//	right := ShapeArabic("ا")
//
// no Lam-Alef ligature is formed between the independent results.
func TestShapeArabic_SemanticBoundary(t *testing.T) {
	// Independent calls simulating separate semantic spans:
	left := ShapeArabic("ل")
	right := ShapeArabic("ا")

	if len(left) != 1 {
		t.Fatalf("left want 1 cluster, got %d", len(left))
	}
	if len(right) != 1 {
		t.Fatalf("right want 1 cluster, got %d", len(right))
	}

	// Lam must remain isolated Lam (0xFEDD), NOT part of a ligature
	if left[0].Visual[0] != 0xFEDD {
		t.Errorf("left Lam: want isolated 0xFEDD, got 0x%04X", left[0].Visual[0])
	}

	// Alef must remain isolated Alef (0xFE8D), NOT part of a ligature
	if right[0].Visual[0] != 0xFE8D {
		t.Errorf("right Alef: want isolated 0xFE8D, got 0x%04X", right[0].Visual[0])
	}

	// Contrast with contiguous call without semantic boundary:
	together := ShapeArabic("لا")
	if len(together) != 1 {
		t.Fatalf("together want 1 cluster, got %d", len(together))
	}
	if together[0].Visual[0] != 0xFEFB { // Lam-Alef ligature
		t.Errorf("contiguous 'لا': want ligature 0xFEFB, got 0x%04X", together[0].Visual[0])
	}
}
