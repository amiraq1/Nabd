package rtl

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestArabicAlphabetCoverage verifies that every standard Arabic letter from
// U+0621 to U+064A has a well-defined Joining_Type and the correct Presentation Forms-B.
func TestArabicAlphabetCoverage(t *testing.T) {
	// Standard Arabic alphabet definitions (U+0621 through U+064A).
	type letterExpectation struct {
		name       string
		jt         joiningClass
		hasForms   bool
		isDual     bool
		isRight    bool
		isNon      bool
		isAlefMaks bool
	}

	expected := map[rune]letterExpectation{
		0x0621: {name: "HAMZA", jt: joiningClassNon, hasForms: true, isNon: true},
		0x0622: {name: "ALEF_MADDA", jt: joiningClassRight, hasForms: true, isRight: true},
		0x0623: {name: "ALEF_HAMZA_ABOVE", jt: joiningClassRight, hasForms: true, isRight: true},
		0x0624: {name: "WAW_HAMZA", jt: joiningClassRight, hasForms: true, isRight: true},
		0x0625: {name: "ALEF_HAMZA_BELOW", jt: joiningClassRight, hasForms: true, isRight: true},
		0x0626: {name: "YEH_HAMZA", jt: joiningClassDual, hasForms: true, isDual: true},
		0x0627: {name: "ALEF", jt: joiningClassRight, hasForms: true, isRight: true},
		0x0628: {name: "BEH", jt: joiningClassDual, hasForms: true, isDual: true},
		0x0629: {name: "TEH_MARBUTA", jt: joiningClassRight, hasForms: true, isRight: true},
		0x062A: {name: "TEH", jt: joiningClassDual, hasForms: true, isDual: true},
		0x062B: {name: "THEH", jt: joiningClassDual, hasForms: true, isDual: true},
		0x062C: {name: "JEEM", jt: joiningClassDual, hasForms: true, isDual: true},
		0x062D: {name: "HAH", jt: joiningClassDual, hasForms: true, isDual: true},
		0x062E: {name: "KHAH", jt: joiningClassDual, hasForms: true, isDual: true},
		0x062F: {name: "DAL", jt: joiningClassRight, hasForms: true, isRight: true},
		0x0630: {name: "THAL", jt: joiningClassRight, hasForms: true, isRight: true},
		0x0631: {name: "REH", jt: joiningClassRight, hasForms: true, isRight: true},
		0x0632: {name: "ZAIN", jt: joiningClassRight, hasForms: true, isRight: true},
		0x0633: {name: "SEEN", jt: joiningClassDual, hasForms: true, isDual: true},
		0x0634: {name: "SHEEN", jt: joiningClassDual, hasForms: true, isDual: true},
		0x0635: {name: "SAD", jt: joiningClassDual, hasForms: true, isDual: true},
		0x0636: {name: "DAD", jt: joiningClassDual, hasForms: true, isDual: true},
		0x0637: {name: "TAH", jt: joiningClassDual, hasForms: true, isDual: true},
		0x0638: {name: "ZAH", jt: joiningClassDual, hasForms: true, isDual: true},
		0x0639: {name: "AIN", jt: joiningClassDual, hasForms: true, isDual: true},
		0x063A: {name: "GHAIN", jt: joiningClassDual, hasForms: true, isDual: true},
		0x0640: {name: "TATWEEL", jt: joiningClassCausing, hasForms: false},
		0x0641: {name: "FEH", jt: joiningClassDual, hasForms: true, isDual: true},
		0x0642: {name: "QAF", jt: joiningClassDual, hasForms: true, isDual: true},
		0x0643: {name: "KAF", jt: joiningClassDual, hasForms: true, isDual: true},
		0x0644: {name: "LAM", jt: joiningClassDual, hasForms: true, isDual: true},
		0x0645: {name: "MEEM", jt: joiningClassDual, hasForms: true, isDual: true},
		0x0646: {name: "NOON", jt: joiningClassDual, hasForms: true, isDual: true},
		0x0647: {name: "HEH", jt: joiningClassDual, hasForms: true, isDual: true},
		0x0648: {name: "WAW", jt: joiningClassRight, hasForms: true, isRight: true},
		0x0649: {name: "ALEF_MAKSURA", jt: joiningClassDual, hasForms: true, isAlefMaks: true},
		0x064A: {name: "YEH", jt: joiningClassDual, hasForms: true, isDual: true},
	}

	// Sort runes for deterministic test reporting.
	runes := make([]rune, 0, len(expected))
	for r := range expected {
		runes = append(runes, r)
	}
	for i := 0; i < len(runes)-1; i++ {
		for j := i + 1; j < len(runes); j++ {
			if runes[i] > runes[j] {
				runes[i], runes[j] = runes[j], runes[i]
			}
		}
	}

	for _, r := range runes {
		exp := expected[r]

		// 1. Verify joiningType.
		gotJT := joiningType(r)
		if gotJT != exp.jt {
			t.Errorf("U+%04X (%s): joiningType want %d, got %d", r, exp.name, exp.jt, gotJT)
		}

		// 2. Verify arabicFormOf.
		form, hasForm := arabicFormOf(r)
		if hasForm != exp.hasForms {
			t.Errorf("U+%04X (%s): arabicFormOf presence want %v, got %v", r, exp.name, exp.hasForms, hasForm)
			continue
		}

		if !exp.hasForms {
			continue
		}

		// Verify Presentation Forms-B ranges for non-zero glyphs.
		checkRange := func(name string, glyph rune) {
			if glyph != 0 && (glyph < 0xFE70 || glyph > 0xFEFF) {
				t.Errorf("U+%04X (%s) form %s (U+%04X) out of Pres-B range [FE70, FEFF]", r, exp.name, name, glyph)
			}
		}
		checkRange("isolated", form.isolated)
		checkRange("final", form.final)
		checkRange("initial", form.initial)
		checkRange("medial", form.medial)

		switch {
		case exp.isNon:
			// Non-joining (Hamza): isolated only.
			if !form.HasIsolated() || form.HasFinal() || form.HasInitial() || form.HasMedial() {
				t.Errorf("U+%04X (%s): non-joining letter must have isolated only, got %+v", r, exp.name, form)
			}
		case exp.isRight:
			// Right-joining: isolated and final only.
			if !form.HasIsolated() || !form.HasFinal() || form.HasInitial() || form.HasMedial() {
				t.Errorf("U+%04X (%s): right-joining letter must have isolated+final only, got %+v", r, exp.name, form)
			}
		case exp.isDual:
			// Dual-joining: all four forms.
			if !form.HasIsolated() || !form.HasFinal() || !form.HasInitial() || !form.HasMedial() {
				t.Errorf("U+%04X (%s): dual-joining letter must have all 4 forms, got %+v", r, exp.name, form)
			}
		case exp.isAlefMaks:
			// Alef Maksura: isolated and final only (Unicode 17 JT=D gap).
			if !form.HasIsolated() || !form.HasFinal() || form.HasInitial() || form.HasMedial() {
				t.Errorf("U+%04X (%s): Alef Maksura must have isolated+final only, got %+v", r, exp.name, form)
			}
		}
	}

	// Verify extended Arabic letters U+063B..U+063F:
	// They have Joining_Type=D but are absent from Presentation Forms-B and recorded in missingForms.
	for r := rune(0x063B); r <= 0x063F; r++ {
		if jt := joiningType(r); jt != joiningClassDual {
			t.Errorf("extended U+%04X joiningType: want Dual, got %d", r, jt)
		}
		if _, ok := arabicFormOf(r); ok {
			t.Errorf("extended U+%04X should not be in Presentation Forms-B", r)
		}
		if !isMissingForm(r) {
			t.Errorf("extended U+%04X should be in missingForms", r)
		}
	}
}

// TestAlefMaksuraContract tests the explicit behavioral contract for U+0649
// (Alef Maksura): JT=D in Unicode 17, but only isolated and final forms in Pres-B.
// When initial or medial form is requested, the Logical Preservation Contract
// keeps the rune as U+0649 without conversion.
func TestAlefMaksuraContract(t *testing.T) {
	r := rune(0x0649)

	// 1. Unicode Joining_Type is Dual.
	if jt := joiningType(r); jt != joiningClassDual {
		t.Fatalf("U+0649 joiningType: want Dual, got %d", jt)
	}

	// 2. Presentation Forms-B entries: isolated (FEEF) and final (FEF0).
	form, ok := arabicFormOf(r)
	if !ok {
		t.Fatalf("U+0649 arabicFormOf: want ok=true, got false")
	}
	if form.isolated != 0xFEEF {
		t.Errorf("U+0649 isolated: want 0xFEEF, got 0x%04X", form.isolated)
	}
	if form.final != 0xFEF0 {
		t.Errorf("U+0649 final: want 0xFEF0, got 0x%04X", form.final)
	}
	if form.initial != 0 {
		t.Errorf("U+0649 initial: want 0, got 0x%04X", form.initial)
	}
	if form.medial != 0 {
		t.Errorf("U+0649 medial: want 0, got 0x%04X", form.medial)
	}

	// 3. Helper predicates.
	if !form.HasIsolated() || !form.HasFinal() || form.HasInitial() || form.HasMedial() {
		t.Errorf("U+0649 helper predicates mismatch: isolated=%v final=%v initial=%v medial=%v",
			form.HasIsolated(), form.HasFinal(), form.HasInitial(), form.HasMedial())
	}

	// 4. Missing forms tracking.
	if !isMissingForm(r) {
		t.Errorf("U+0649 must be recorded in missingForms")
	}

	// 5. Logical Preservation Contract via shapedRune:
	// Isolated: shaped to 0xFEEF
	if got := shapedRune(r, false, false); got != 0xFEEF {
		t.Errorf("shapedRune(U+0649, isolated): want 0xFEEF, got 0x%04X", got)
	}
	// Final: shaped to 0xFEF0
	if got := shapedRune(r, true, false); got != 0xFEF0 {
		t.Errorf("shapedRune(U+0649, final): want 0xFEF0, got 0x%04X", got)
	}
	// Initial: preserved as logical U+0649
	if got := shapedRune(r, false, true); got != 0x0649 {
		t.Errorf("shapedRune(U+0649, initial): want logical 0x0649, got 0x%04X", got)
	}
	// Medial: preserved as logical U+0649
	if got := shapedRune(r, true, true); got != 0x0649 {
		t.Errorf("shapedRune(U+0649, medial): want logical 0x0649, got 0x%04X", got)
	}
}

// TestLamAlefLigatures tests all four canonical Lam-Alef ligature combinations:
//   - Lam + Alef with Madda (0622) → isolated FEF5, final FEF6
//   - Lam + Alef with Hamza above (0623) → isolated FEF7, final FEF8
//   - Lam + Alef with Hamza below (0625) → isolated FEF9, final FEFA
//   - Lam + Plain Alef (0627) → isolated FEFB, final FEFC
func TestLamAlefLigatures(t *testing.T) {
	cases := []struct {
		alef         rune
		wantIsolated rune
		wantFinal    rune
	}{
		{0x0622, 0xFEF5, 0xFEF6},
		{0x0623, 0xFEF7, 0xFEF8},
		{0x0625, 0xFEF9, 0xFEFA},
		{0x0627, 0xFEFB, 0xFEFC},
	}

	for _, c := range cases {
		iso, fin, ok := lamAlefFormOf(c.alef)
		if !ok {
			t.Errorf("lamAlefFormOf(0x%04X): expected ok=true", c.alef)
			continue
		}
		if iso != c.wantIsolated {
			t.Errorf("lamAlefFormOf(0x%04X) isolated: want 0x%04X, got 0x%04X", c.alef, c.wantIsolated, iso)
		}
		if fin != c.wantFinal {
			t.Errorf("lamAlefFormOf(0x%04X) final: want 0x%04X, got 0x%04X", c.alef, c.wantFinal, fin)
		}
	}

	// Non-alef characters must return ok=false.
	invalidAlefs := []rune{0x0628, 0x0644, 0x0649, 0x064A, 'A', 0x0020}
	for _, r := range invalidAlefs {
		if _, _, ok := lamAlefFormOf(r); ok {
			t.Errorf("lamAlefFormOf(0x%04X) should return ok=false for non-Alef", r)
		}
	}
}

// TestTransparentDiacritics verifies that Arabic Tashkeel/Harakat marks are
// classified as joiningClassTrans (transparent) and have no base letter forms.
func TestTransparentDiacritics(t *testing.T) {
	harakat := []struct {
		r    rune
		name string
	}{
		{0x064B, "FATHATAN"},
		{0x064C, "DAMMATAN"},
		{0x064D, "KASRATAN"},
		{0x064E, "FATHA"},
		{0x064F, "DAMMA"},
		{0x0650, "KASRA"},
		{0x0651, "SHADDA"},
		{0x0652, "SUKUN"},
		{0x0653, "MADDAH_ABOVE"},
		{0x0654, "HAMZA_ABOVE"},
		{0x0655, "HAMZA_BELOW"},
		{0x0670, "SUPERSCRIPT_ALEF"},
	}

	for _, h := range harakat {
		if jt := joiningType(h.r); jt != joiningClassTrans {
			t.Errorf("Tashkeel U+%04X (%s): want joiningClassTrans, got %d", h.r, h.name, jt)
		}
		if _, ok := arabicFormOf(h.r); ok {
			t.Errorf("Tashkeel U+%04X (%s) should not have a base letter presentation form", h.r, h.name)
		}
	}
}

// TestMissingFormsTable verifies that missingForms accurately catalogues all
// dual-joining characters that lack Presentation Forms-B initial/medial glyphs.
func TestMissingFormsTable(t *testing.T) {
	if len(missingForms) == 0 {
		t.Fatal("missingForms table must not be empty")
	}

	// 1. Table must be strictly sorted by base rune for binary search.
	for i := 1; i < len(missingForms); i++ {
		if missingForms[i-1].base >= missingForms[i].base {
			t.Errorf("missingForms unsorted at index %d: U+%04X >= U+%04X",
				i, missingForms[i-1].base, missingForms[i].base)
		}
	}

	// 2. Every entry must be JT=D and lack initial/medial forms.
	for _, m := range missingForms {
		jt := joiningType(m.base)
		if jt != joiningClassDual {
			t.Errorf("missingForms entry U+%04X has joiningType %d, want Dual", m.base, jt)
		}
		if !isMissingForm(m.base) {
			t.Errorf("isMissingForm(U+%04X) returned false for entry in missingForms", m.base)
		}
		if form, ok := arabicFormOf(m.base); ok {
			if form.initial != 0 || form.medial != 0 {
				t.Errorf("missingForms entry U+%04X has non-zero initial/medial in Pres-B: %+v", m.base, form)
			}
		}
	}

	// 3. Known extended letters are tracked.
	knownMissing := []rune{
		0x0649, // Alef Maksura (Arabic)
		0x067E, // Peh (Persian/Urdu)
		0x0686, // Tcheh (Persian/Urdu)
		0x06AF, // Gaf (Persian/Urdu)
		0x06CC, // Farsi Yeh
	}
	for _, r := range knownMissing {
		if !isMissingForm(r) {
			t.Errorf("expected U+%04X to be in missingForms", r)
		}
	}
}

// TestArabicFormsSorted verifies that arabicForms is strictly sorted by base rune
// for correct binary search.
func TestArabicFormsSorted(t *testing.T) {
	for i := 1; i < len(arabicForms); i++ {
		if arabicForms[i-1].base >= arabicForms[i].base {
			t.Errorf("arabicForms unsorted at index %d: U+%04X >= U+%04X",
				i, arabicForms[i-1].base, arabicForms[i].base)
		}
	}
}

// TestShapedRuneContract verifies the Logical Preservation Contract across
// different character categories.
func TestShapedRuneContract(t *testing.T) {
	// 1. Dual-joining letter (0x0628 Beh) with all 4 forms.
	beh := rune(0x0628)
	if got := shapedRune(beh, false, false); got != 0xFE8F {
		t.Errorf("Beh isolated: want 0xFE8F, got 0x%04X", got)
	}
	if got := shapedRune(beh, true, false); got != 0xFE90 {
		t.Errorf("Beh final: want 0xFE90, got 0x%04X", got)
	}
	if got := shapedRune(beh, false, true); got != 0xFE91 {
		t.Errorf("Beh initial: want 0xFE91, got 0x%04X", got)
	}
	if got := shapedRune(beh, true, true); got != 0xFE92 {
		t.Errorf("Beh medial: want 0xFE92, got 0x%04X", got)
	}

	// 2. Right-joining letter (0x0627 Alef):
	// - Isolated and final are shaped.
	// - Initial and medial have no glyph (0) -> preserved as logical 0x0627.
	alef := rune(0x0627)
	if got := shapedRune(alef, false, false); got != 0xFE8D {
		t.Errorf("Alef isolated: want 0xFE8D, got 0x%04X", got)
	}
	if got := shapedRune(alef, true, false); got != 0xFE8E {
		t.Errorf("Alef final: want 0xFE8E, got 0x%04X", got)
	}
	if got := shapedRune(alef, false, true); got != 0x0627 {
		t.Errorf("Alef initial: want logical 0x0627, got 0x%04X", got)
	}
	if got := shapedRune(alef, true, true); got != 0x0627 {
		t.Errorf("Alef medial: want logical 0x0627, got 0x%04X", got)
	}

	// 3. Characters outside Presentation Forms-B: preserved as logical rune.
	for _, unmapped := range []rune{'A', '1', 0x0640, 0x067E, 0x06CC} {
		if got := shapedRune(unmapped, true, true); got != unmapped {
			t.Errorf("unmapped U+%04X: want logical rune, got 0x%04X", unmapped, got)
		}
	}
}

// TestOutOfRangeRunes verifies that runes outside U+0600–U+06FF return safe defaults.
func TestOutOfRangeRunes(t *testing.T) {
	for _, r := range []rune{0x0020, 0x0041, 0x05FF, 0x0700, 0xFE70, 0x1F600} {
		if jt := joiningType(r); jt != joiningClassNon {
			t.Errorf("out-of-range U+%04X joiningType: want Non, got %d", r, jt)
		}
		if _, ok := arabicFormOf(r); ok {
			t.Errorf("out-of-range U+%04X should not have arabicFormOf", r)
		}
		if isMissingForm(r) {
			t.Errorf("out-of-range U+%04X should not be in missingForms", r)
		}
	}
}

// TestArabicShapingExplicitRecordAudit performs a strict 1-to-1 audit against
// every explicit data record in ArabicShaping.txt, verifying Joining_Type and
// Joining_Group properties, visual mapping availability, and out-of-range tracking.
func TestArabicShapingExplicitRecordAudit(t *testing.T) {
	path := filepath.Join("testdata", "unicode", "17.0.0", "ArabicShaping.txt")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening ArabicShaping.txt: %v", err)
	}
	defer f.Close()

	var explicitEntries int
	var joiningTypesMatched int
	var joiningGroupsMatched int
	var visualMappingAvailable int
	var visualMappingUnavailable int
	var outside0600_06FF int

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, ";")
		if len(parts) < 4 {
			continue
		}
		explicitEntries++

		cp64, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 16, 32)
		if err != nil {
			t.Fatalf("parsing codepoint %q: %v", parts[0], err)
		}
		cp := rune(cp64)

		if cp < 0x0600 || cp > 0x06FF {
			outside0600_06FF++
		}

		sourceJT := strings.TrimSpace(parts[2])
		genJT := joiningType(cp).String()
		if genJT == sourceJT {
			joiningTypesMatched++
		} else {
			fmt.Printf("U+%04X field=Joining_Type source=%s generated=%s\n", cp, sourceJT, genJT)
		}

		sourceJG := strings.TrimSpace(parts[3])
		genJG := joiningGroup(cp)
		if genJG == sourceJG {
			joiningGroupsMatched++
		} else {
			fmt.Printf("U+%04X field=Joining_Group source=%s generated=%s\n", cp, sourceJG, genJG)
		}

		form, ok := arabicFormOf(cp)
		if ok && (form.HasIsolated() || form.HasFinal() || form.HasInitial() || form.HasMedial()) {
			visualMappingAvailable++
		} else {
			visualMappingUnavailable++
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading ArabicShaping.txt: %v", err)
	}

	if joiningTypesMatched != explicitEntries {
		t.Errorf("JOINING_TYPES_MATCHED (%d) != EXPLICIT_ENTRIES (%d)", joiningTypesMatched, explicitEntries)
	}
	if joiningGroupsMatched != explicitEntries {
		t.Errorf("JOINING_GROUPS_MATCHED (%d) != EXPLICIT_ENTRIES (%d)", joiningGroupsMatched, explicitEntries)
	}
	if visualMappingAvailable+visualMappingUnavailable != explicitEntries {
		t.Errorf("VISUAL_MAPPING_AVAILABLE (%d) + VISUAL_MAPPING_UNAVAILABLE (%d) != EXPLICIT_ENTRIES (%d)",
			visualMappingAvailable, visualMappingUnavailable, explicitEntries)
	}

	res := "PASS"
	if t.Failed() {
		res = "FAIL"
	}

	// Exact requested output banner:
	fmt.Println("=== ArabicShaping explicit-record audit ===")
	fmt.Printf("EXPLICIT_ENTRIES=%d\n", explicitEntries)
	fmt.Printf("JOINING_TYPES_MATCHED=%d\n", joiningTypesMatched)
	fmt.Printf("JOINING_GROUPS_MATCHED=%d\n", joiningGroupsMatched)
	fmt.Printf("VISUAL_MAPPING_AVAILABLE=%d\n", visualMappingAvailable)
	fmt.Printf("VISUAL_MAPPING_UNAVAILABLE=%d\n", visualMappingUnavailable)
	fmt.Printf("OUTSIDE_0600_06FF=%d\n", outside0600_06FF)
	fmt.Printf("RESULT=%s\n", res)
}
