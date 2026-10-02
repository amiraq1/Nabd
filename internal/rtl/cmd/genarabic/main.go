// Command genarabic generates internal/rtl/arabic_tables_generated.go and
// internal/rtl/arabic_pres_tables.go.
//
// arabic_tables_generated.go is derived from the Unicode 17.0.0 data files:
//
//   - internal/rtl/testdata/unicode/17.0.0/UnicodeData.txt
//     (decomposition mappings → Presentation Forms-B entries)
//   - internal/rtl/testdata/unicode/17.0.0/ArabicShaping.txt
//     (Joining_Type and Joining_Group properties)
//
// Usage:
//
//	go run ./internal/rtl/cmd/genarabic
//
// The generator is deterministic: given the same input SHA-256 values it
// always produces byte-for-byte identical output. Run it twice and compare
// to confirm.
//
// Design constraints (from provenance.md "Arabic shaping gate for PR 2"):
//
//   - Only Prose clusters are shaped; Code/Path/URL spans are never touched.
//   - Presentation Forms are written to Cluster.Text only; they never enter
//     the journal, search indexes, or RestoreFromSource output.
//   - Letters whose Joining_Type is D but that have no independent
//     initial/medial Presentation Forms-B entries (e.g. U+0649 Alef Maksura)
//     are recorded in the missingForms table and treated according to the
//     Logical Preservation Contract (missing forms remain the logical rune).
//     This is an explicit, tested contract rather than a silent gap.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// ── Expected SHA-256 checksums ────────────────────────────────────────────────

// expectedUnicodeDataSHA256 is the SHA-256 of UnicodeData-17.0.0.txt.
const expectedUnicodeDataSHA256 = "2e1efc1dcb59c575eedf5ccae60f95229f706ee6d031835247d843c11d96470c"

// expectedArabicShapingSHA256 is the SHA-256 of ArabicShaping-17.0.0.txt.
const expectedArabicShapingSHA256 = "39afa01e680e27d0fd10b67a9b27be13fbaa3d0efecfb5be45991de9a0d267d0"

// ── Arabic block boundaries ───────────────────────────────────────────────────

const arabicBlockLo = 0x0600
const arabicBlockHi = 0x06FF

// presFormsLo / presFormsHi is the Arabic Presentation Forms-B block.
const presFormsLo = 0xFE70
const presFormsHi = 0xFEFF

// ── Data structures ───────────────────────────────────────────────────────────

// joiningClass mirrors the Unicode Joining_Type property values we care about.
type joiningClass byte

const (
	jtNon     joiningClass = 'U' // non-joining
	jtDual    joiningClass = 'D' // dual-joining
	jtRight   joiningClass = 'R' // right-joining
	jtLeft    joiningClass = 'L' // left-joining
	jtCausing joiningClass = 'C' // join-causing (Tatweel, ZWJ)
	jtTrans   joiningClass = 'T' // transparent (diacritics)
)

// formSet holds the four Presentation Forms-B code points for one base letter.
// Zero means the form is absent in the Pres-B block.
type formSet struct {
	isolated, final, initial, medial rune
}

// lamAlefPair maps one Alef variant to its lam-alef ligature forms.
type lamAlefPair struct {
	alef     rune
	isolated rune
	final    rune
}

// missingFormEntry records a letter that is Joining_Type D but lacks
// independent initial/medial Presentation Forms-B glyphs.
type missingFormEntry struct {
	base rune
	note string
}

func main() {
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile is …/internal/rtl/cmd/genarabic/main.go
	// repo root is four levels up
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	dataDir := filepath.Join(repoRoot, "internal", "rtl", "testdata", "unicode", "17.0.0")
	outTablesFile := filepath.Join(repoRoot, "internal", "rtl", "arabic_tables_generated.go")
	outPresFile := filepath.Join(repoRoot, "internal", "rtl", "arabic_pres_tables.go")

	unicodeDataFile := filepath.Join(dataDir, "UnicodeData.txt")
	arabicShapingFile := filepath.Join(dataDir, "ArabicShaping.txt")

	// 1. Verify checksums.
	if err := verifySHA256(unicodeDataFile, expectedUnicodeDataSHA256); err != nil {
		fatalf("genarabic: %v", err)
	}
	if err := verifySHA256(arabicShapingFile, expectedArabicShapingSHA256); err != nil {
		fatalf("genarabic: %v", err)
	}

	// 2. Parse joining types and groups from ArabicShaping.txt
	// and supplement unlisted combining marks from UnicodeData.txt (JT=T).
	joiningTypes, joiningGroups, err := parseJoiningData(arabicShapingFile, unicodeDataFile)
	if err != nil {
		fatalf("genarabic: parse joining data: %v", err)
	}

	// 3. Derive Presentation Forms from UnicodeData.txt decomposition mappings.
	forms, lamAlefs, missing, err := deriveForms(unicodeDataFile, joiningTypes)
	if err != nil {
		fatalf("genarabic: derive forms: %v", err)
	}

	// 4. Generate Go source.
	src, err := generate(forms, lamAlefs, missing, joiningTypes, joiningGroups)
	if err != nil {
		fatalf("genarabic: generate: %v", err)
	}
	presSrc, err := generatePresTables()
	if err != nil {
		fatalf("genarabic: generate pres tables: %v", err)
	}

	if err := os.WriteFile(outTablesFile, src, 0o644); err != nil {
		fatalf("genarabic: write %s: %v", outTablesFile, err)
	}
	if err := os.WriteFile(outPresFile, presSrc, 0o644); err != nil {
		fatalf("genarabic: write %s: %v", outPresFile, err)
	}
	fmt.Printf("genarabic: wrote %s (%d bytes)\n", outTablesFile, len(src))
	fmt.Printf("genarabic: wrote %s (%d bytes)\n", outPresFile, len(presSrc))
	fmt.Printf("genarabic: %d base letters, %d lam-alef ligatures, %d missing-form entries, %d joining types, %d joining groups\n",
		len(forms), len(lamAlefs), len(missing), len(joiningTypes), len(joiningGroups))
}

func fatalf(f string, a ...any) {
	fmt.Fprintf(os.Stderr, f+"\n", a...)
	os.Exit(1)
}

// ── SHA-256 verification ──────────────────────────────────────────────────────

func verifySHA256(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("SHA-256 mismatch for %s:\n  want %s\n   got %s", path, want, got)
	}
	return nil
}

// ── Parse Joining Types and Groups ───────────────────────────────────────────

// parseJoiningData parses ArabicShaping.txt for all explicit Joining_Type and
// Joining_Group records, and supplements unlisted combining marks from UnicodeData.txt
// according to the normative Unicode rule:
//
//	"Code points that are not explicitly listed in this file are either of
//	 Joining_Type T or U:
//	 - Those that are not explicitly listed and that are of General_Category Mn or Me
//	   are Joining_Type=T.
//	 - All others not explicitly listed are Joining_Type=U."
func parseJoiningData(shapingPath, unicodeDataPath string) (map[rune]joiningClass, map[rune]string, error) {
	f, err := os.Open(shapingPath)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	jt := make(map[rune]joiningClass)
	jg := make(map[rune]string)

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
		cp64, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 16, 32)
		if err != nil {
			continue
		}
		cp := rune(cp64)
		switch strings.TrimSpace(parts[2]) {
		case "D":
			jt[cp] = jtDual
		case "R":
			jt[cp] = jtRight
		case "L":
			jt[cp] = jtLeft
		case "C":
			jt[cp] = jtCausing
		case "U":
			jt[cp] = jtNon
		case "T":
			jt[cp] = jtTrans
		}
		jg[cp] = strings.TrimSpace(parts[3])
	}
	if err := sc.Err(); err != nil {
		return nil, nil, err
	}

	uf, err := os.Open(unicodeDataPath)
	if err != nil {
		return nil, nil, err
	}
	defer uf.Close()

	usc := bufio.NewScanner(uf)
	for usc.Scan() {
		line := usc.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, ";")
		if len(fields) < 3 {
			continue
		}
		cp64, err := strconv.ParseInt(strings.TrimSpace(fields[0]), 16, 32)
		if err != nil || cp64 < arabicBlockLo || cp64 > arabicBlockHi {
			continue
		}
		cp := rune(cp64)
		if _, exists := jt[cp]; !exists {
			cat := strings.TrimSpace(fields[2])
			// Strictly Mn (Nonspacing_Mark) and Me (Enclosing_Mark) default to Transparent.
			// No generalization from Cf: format controls must come from ArabicShaping.txt.
			if cat == "Mn" || cat == "Me" {
				jt[cp] = jtTrans
			}
		}
	}
	return jt, jg, usc.Err()
}

// ── Derive forms from UnicodeData.txt ────────────────────────────────────────

// deriveForms reads UnicodeData.txt and builds:
//   - forms: map from Arabic base letter → formSet (Presentation Forms-B)
//   - lamAlefs: lam-alef ligature pairs (decomp of the ligature gives base=U+0644)
//   - missing: letters that are JT=D but have no initial/medial Pres-B forms
func deriveForms(
	path string,
	joiningTypes map[rune]joiningClass,
) (
	forms map[rune]formSet,
	lamAlefs []lamAlefPair,
	missing []missingFormEntry,
	err error,
) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, nil, err
	}
	defer f.Close()

	// raw[base] accumulates all Pres-B forms found for that base letter.
	type rawForms struct {
		isolated, final, initial, medial rune
	}
	raw := make(map[rune]*rawForms)

	// lamAlefMap: alef-variant → (isolated, final) ligature.
	// Lam-Alef ligatures have a 2-codepoint decomp: <compat> 0644 <alef>.
	type lamAlefRaw struct{ isolated, final rune }
	lamAlefMap := make(map[rune]lamAlefRaw)

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, ";")
		if len(fields) < 6 {
			continue
		}
		cp64, err2 := strconv.ParseInt(strings.TrimSpace(fields[0]), 16, 32)
		if err2 != nil {
			continue
		}
		cp := rune(cp64)
		if cp < presFormsLo || cp > presFormsHi {
			continue
		}
		decomp := strings.TrimSpace(fields[5])
		if decomp == "" {
			continue
		}

		// Parse decomposition: <tag> base [extra]
		// e.g. "<isolated> 0628" or "<isolated> 0644 0627"
		if !strings.HasPrefix(decomp, "<") {
			continue
		}
		gt := strings.Index(decomp, ">")
		if gt < 0 {
			continue
		}
		tag := decomp[1:gt]
		rest := strings.TrimSpace(decomp[gt+1:])
		cpStrs := strings.Fields(rest)
		if len(cpStrs) == 0 {
			continue
		}

		switch len(cpStrs) {
		case 1:
			// Single code point decomposition: this is a base letter form.
			base64, err2 := strconv.ParseInt(cpStrs[0], 16, 32)
			if err2 != nil {
				continue
			}
			base := rune(base64)
			// Only record forms whose base is in the Arabic block 0600-06FF.
			if base < arabicBlockLo || base > arabicBlockHi {
				continue
			}
			if raw[base] == nil {
				raw[base] = &rawForms{}
			}
			rf := raw[base]
			switch tag {
			case "isolated":
				rf.isolated = cp
			case "final":
				rf.final = cp
			case "initial":
				rf.initial = cp
			case "medial":
				rf.medial = cp
			}
		case 2:
			// Two code point decomposition: check for Lam-Alef ligatures (<tag> 0644 <alef>).
			base64, err2 := strconv.ParseInt(cpStrs[0], 16, 32)
			if err2 != nil || rune(base64) != 0x0644 {
				// Non-Lam multi-char decompositions (e.g. Tatweel+Tashkeel <medial> 0640 064B
				// or space+Tashkeel <isolated> 0020 064B) are diacritic glyphs, not letter forms.
				continue
			}
			alef64, err2 := strconv.ParseInt(cpStrs[1], 16, 32)
			if err2 != nil {
				continue
			}
			alef := rune(alef64)
			la := lamAlefMap[alef]
			switch tag {
			case "isolated":
				la.isolated = cp
			case "final":
				la.final = cp
			}
			lamAlefMap[alef] = la
		}
	}
	if err2 := sc.Err(); err2 != nil {
		return nil, nil, nil, err2
	}

	// Build sorted forms map.
	forms = make(map[rune]formSet, len(raw))
	for base, rf := range raw {
		forms[base] = formSet{
			isolated: rf.isolated,
			final:    rf.final,
			initial:  rf.initial,
			medial:   rf.medial,
		}
	}

	// Build sorted lam-alef slice.
	for alef, la := range lamAlefMap {
		if la.isolated != 0 || la.final != 0 {
			lamAlefs = append(lamAlefs, lamAlefPair{alef: alef, isolated: la.isolated, final: la.final})
		}
	}
	sort.Slice(lamAlefs, func(i, j int) bool { return lamAlefs[i].alef < lamAlefs[j].alef })

	// Identify missing-form entries: JT=D in Arabic block 0600-06FF but no initial/medial in Pres-B.
	for base, jt := range joiningTypes {
		if base < arabicBlockLo || base > arabicBlockHi {
			continue
		}
		if jt != jtDual {
			continue
		}
		fs, ok := forms[base]
		if !ok || (fs.initial == 0 && fs.medial == 0) {
			note := "JT=D but Presentation Forms-B block has no initial/medial glyphs"
			if !ok {
				note = "JT=D but absent from Presentation Forms-B block entirely"
			}
			missing = append(missing, missingFormEntry{base: base, note: note})
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i].base < missing[j].base })

	return forms, lamAlefs, missing, nil
}

// ── Code generation ───────────────────────────────────────────────────────────

func generate(
	forms map[rune]formSet,
	lamAlefs []lamAlefPair,
	missing []missingFormEntry,
	joiningTypes map[rune]joiningClass,
	joiningGroups map[rune]string,
) ([]byte, error) {

	// Sort base letters for deterministic output.
	bases := make([]rune, 0, len(forms))
	for base := range forms {
		bases = append(bases, base)
	}
	sort.Slice(bases, func(i, j int) bool { return bases[i] < bases[j] })

	// Sort joining-type entries.
	type jtEntry struct {
		cp rune
		jt joiningClass
	}
	jtEntries := make([]jtEntry, 0, len(joiningTypes))
	for cp, jt := range joiningTypes {
		jtEntries = append(jtEntries, jtEntry{cp, jt})
	}
	sort.Slice(jtEntries, func(i, j int) bool { return jtEntries[i].cp < jtEntries[j].cp })

	// Sort joining-group entries (only non-"No_Joining_Group").
	type jgEntry struct {
		cp rune
		jg string
	}
	jgEntries := make([]jgEntry, 0, len(joiningGroups))
	for cp, jg := range joiningGroups {
		if jg != "No_Joining_Group" {
			jgEntries = append(jgEntries, jgEntry{cp, jg})
		}
	}
	sort.Slice(jgEntries, func(i, j int) bool { return jgEntries[i].cp < jgEntries[j].cp })

	var buf bytes.Buffer

	// ── File header ───────────────────────────────────────────────────────────
	buf.WriteString("// Code generated by internal/rtl/cmd/genarabic — DO NOT EDIT.\n")
	buf.WriteString("//\n")
	buf.WriteString("// Sources (Unicode 17.0.0):\n")
	buf.WriteString("//   UnicodeData.txt    sha256:" + expectedUnicodeDataSHA256 + "\n")
	buf.WriteString("//   ArabicShaping.txt  sha256:" + expectedArabicShapingSHA256 + "\n")
	buf.WriteString("//   https://unicode.org/Public/17.0.0/ucd/UnicodeData.txt\n")
	buf.WriteString("//   https://unicode.org/Public/17.0.0/ucd/ArabicShaping.txt\n")
	buf.WriteString("//\n")
	buf.WriteString("// Regenerate: go run ./internal/rtl/cmd/genarabic\n")
	buf.WriteString("//\n")
	buf.WriteString("// Design constraints (see provenance.md §Arabic shaping gate for PR 2):\n")
	buf.WriteString("//   - Only Prose clusters are shaped; Code/Path/URL are never modified.\n")
	buf.WriteString("//   - Presentation Forms are written to Cluster.Text only; they never\n")
	buf.WriteString("//     enter the journal, search indexes, or RestoreFromSource output.\n")
	buf.WriteString("//   - Letters that are Joining_Type D but lack initial/medial glyphs in\n")
	buf.WriteString("//     Presentation Forms-B are recorded in missingForms and treated according\n")
	buf.WriteString("//     to the Logical Preservation Contract (missing forms remain the logical rune).\n")
	buf.WriteString("\n")
	buf.WriteString("package rtl\n\n")

	// ── joiningClass type, String method, and constants ───────────────────────
	buf.WriteString("// joiningClass is the Unicode Joining_Type property (ArabicShaping.txt).\n")
	buf.WriteString("type joiningClass uint8\n\n")
	buf.WriteString("const (\n")
	buf.WriteString("\tjoiningClassNon     joiningClass = iota // U: non-joining\n")
	buf.WriteString("\tjoiningClassDual                        // D: dual-joining\n")
	buf.WriteString("\tjoiningClassRight                       // R: right-joining\n")
	buf.WriteString("\tjoiningClassLeft                        // L: left-joining\n")
	buf.WriteString("\tjoiningClassCausing                     // C: join-causing (Tatweel, ZWJ)\n")
	buf.WriteString("\tjoiningClassTrans                       // T: transparent (diacritics)\n")
	buf.WriteString(")\n\n")

	buf.WriteString("// String returns the single-letter Unicode identifier for the Joining_Type.\n")
	buf.WriteString("func (c joiningClass) String() string {\n")
	buf.WriteString("\tswitch c {\n")
	buf.WriteString("\tcase joiningClassDual:\n\t\treturn \"D\"\n")
	buf.WriteString("\tcase joiningClassRight:\n\t\treturn \"R\"\n")
	buf.WriteString("\tcase joiningClassLeft:\n\t\treturn \"L\"\n")
	buf.WriteString("\tcase joiningClassCausing:\n\t\treturn \"C\"\n")
	buf.WriteString("\tcase joiningClassTrans:\n\t\treturn \"T\"\n")
	buf.WriteString("\tdefault:\n\t\treturn \"U\"\n")
	buf.WriteString("\t}\n")
	buf.WriteString("}\n\n")

	// ── joiningType function ─────────────────────────────────────────────────
	buf.WriteString("// joiningType returns the Joining_Type for a rune.\n")
	buf.WriteString("// Source: ArabicShaping-17.0.0.txt and UnicodeData-17.0.0.txt (Mn/Me default T).\n")
	buf.WriteString("func joiningType(r rune) joiningClass {\n")
	buf.WriteString("\tswitch r {\n")
	for _, e := range jtEntries {
		var cls string
		switch e.jt {
		case jtDual:
			cls = "joiningClassDual"
		case jtRight:
			cls = "joiningClassRight"
		case jtLeft:
			cls = "joiningClassLeft"
		case jtCausing:
			cls = "joiningClassCausing"
		case jtNon:
			cls = "joiningClassNon"
		case jtTrans:
			cls = "joiningClassTrans"
		default:
			cls = "joiningClassNon"
		}
		fmt.Fprintf(&buf, "\tcase 0x%04X:\n\t\treturn %s\n", e.cp, cls)
	}
	buf.WriteString("\t}\n")
	buf.WriteString("\treturn joiningClassNon\n")
	buf.WriteString("}\n\n")

	// ── joiningGroup function ────────────────────────────────────────────────
	buf.WriteString("// joiningGroup returns the Joining_Group property for r.\n")
	buf.WriteString("// Returns \"No_Joining_Group\" if r has no explicit joining group.\n")
	buf.WriteString("// Source: ArabicShaping-17.0.0.txt\n")
	buf.WriteString("func joiningGroup(r rune) string {\n")
	buf.WriteString("\tswitch r {\n")
	for _, e := range jgEntries {
		fmt.Fprintf(&buf, "\tcase 0x%04X:\n\t\treturn %q\n", e.cp, e.jg)
	}
	buf.WriteString("\t}\n")
	buf.WriteString("\treturn \"No_Joining_Group\"\n")
	buf.WriteString("}\n\n")

	// ── arabicForm type ───────────────────────────────────────────────────────
	buf.WriteString("// arabicForm holds the four Presentation Forms-B code points for one\n")
	buf.WriteString("// Arabic base letter. Zero means the form is absent in the Pres-B block.\n")
	buf.WriteString("// Source: UnicodeData-17.0.0.txt decomposition mappings.\n")
	buf.WriteString("type arabicForm struct {\n")
	buf.WriteString("\tisolated, final, initial, medial rune\n")
	buf.WriteString("}\n\n")

	buf.WriteString("// HasIsolated reports whether an isolated presentation form exists.\n")
	buf.WriteString("func (f arabicForm) HasIsolated() bool { return f.isolated != 0 }\n\n")
	buf.WriteString("// HasFinal reports whether a final presentation form exists.\n")
	buf.WriteString("func (f arabicForm) HasFinal() bool { return f.final != 0 }\n\n")
	buf.WriteString("// HasInitial reports whether an initial presentation form exists.\n")
	buf.WriteString("func (f arabicForm) HasInitial() bool { return f.initial != 0 }\n\n")
	buf.WriteString("// HasMedial reports whether a medial presentation form exists.\n")
	buf.WriteString("func (f arabicForm) HasMedial() bool { return f.medial != 0 }\n\n")

	// ── arabicForms table ─────────────────────────────────────────────────────
	fmt.Fprintf(&buf, "// arabicForms lists the %d base letters that have Presentation Forms-B\n", len(bases))
	buf.WriteString("// entries, sorted by base code point for binary search via arabicFormOf.\n")
	buf.WriteString("var arabicForms = [...]struct {\n")
	buf.WriteString("\tbase rune\n")
	buf.WriteString("\tform arabicForm\n")
	buf.WriteString("}{\n")
	for _, base := range bases {
		fs := forms[base]
		fmt.Fprintf(&buf,
			"\t{0x%04X, arabicForm{0x%04X, 0x%04X, 0x%04X, 0x%04X}},\n",
			base, fs.isolated, fs.final, fs.initial, fs.medial)
	}
	buf.WriteString("}\n\n")

	// ── arabicFormOf lookup ───────────────────────────────────────────────────
	buf.WriteString("// arabicFormOf returns the Presentation Forms-B entry for r.\n")
	buf.WriteString("// ok is false when r has no Presentation Forms-B entries.\n")
	buf.WriteString("func arabicFormOf(r rune) (arabicForm, bool) {\n")
	buf.WriteString("\tlo, hi := 0, len(arabicForms)-1\n")
	buf.WriteString("\tfor lo <= hi {\n")
	buf.WriteString("\t\tmid := (lo + hi) >> 1\n")
	buf.WriteString("\t\tswitch {\n")
	buf.WriteString("\t\tcase arabicForms[mid].base == r:\n")
	buf.WriteString("\t\t\treturn arabicForms[mid].form, true\n")
	buf.WriteString("\t\tcase arabicForms[mid].base < r:\n")
	buf.WriteString("\t\t\tlo = mid + 1\n")
	buf.WriteString("\t\tdefault:\n")
	buf.WriteString("\t\t\thi = mid - 1\n")
	buf.WriteString("\t\t}\n")
	buf.WriteString("\t}\n")
	buf.WriteString("\treturn arabicForm{}, false\n")
	buf.WriteString("}\n\n")

	// ── shapedRune (Logical Preservation Contract) ───────────────────────────
	buf.WriteString("// shapedRune returns the Presentation Forms-B glyph for r according to its\n")
	buf.WriteString("// joining state (rightJoined and leftJoined).\n")
	buf.WriteString("//\n")
	buf.WriteString("// Logical Preservation Contract:\n")
	buf.WriteString("// If the required presentation form glyph is absent (0x0000) or r has no\n")
	buf.WriteString("// presentation forms at all, shapedRune returns r unchanged.\n")
	buf.WriteString("// For example, U+0649 (Alef Maksura, JT=D) has isolated (0xFEEF) and final (0xFEF0)\n")
	buf.WriteString("// forms, but no initial or medial glyphs in Presentation Forms-B; when in initial\n")
	buf.WriteString("// or medial position, it returns 0x0649 unchanged.\n")
	buf.WriteString("func shapedRune(r rune, rightJoined, leftJoined bool) rune {\n")
	buf.WriteString("\tform, ok := arabicFormOf(r)\n")
	buf.WriteString("\tif !ok {\n")
	buf.WriteString("\t\treturn r\n")
	buf.WriteString("\t}\n")
	buf.WriteString("\tvar shaped rune\n")
	buf.WriteString("\tswitch {\n")
	buf.WriteString("\tcase rightJoined && leftJoined:\n")
	buf.WriteString("\t\tshaped = form.medial\n")
	buf.WriteString("\tcase rightJoined && !leftJoined:\n")
	buf.WriteString("\t\tshaped = form.final\n")
	buf.WriteString("\tcase !rightJoined && leftJoined:\n")
	buf.WriteString("\t\tshaped = form.initial\n")
	buf.WriteString("\tdefault:\n")
	buf.WriteString("\t\tshaped = form.isolated\n")
	buf.WriteString("\t}\n")
	buf.WriteString("\tif shaped == 0 {\n")
	buf.WriteString("\t\treturn r\n")
	buf.WriteString("\t}\n")
	buf.WriteString("\treturn shaped\n")
	buf.WriteString("}\n\n")

	// ── lamAlefEntry type and table ───────────────────────────────────────────
	buf.WriteString("// lamAlefEntry maps one Alef variant to its Lam-Alef ligature forms.\n")
	buf.WriteString("// The Lam base (U+0644) is implicit.\n")
	buf.WriteString("type lamAlefEntry struct {\n")
	buf.WriteString("\talef               rune // Alef variant\n")
	buf.WriteString("\tisolated, finalLig rune // ligature glyphs\n")
	buf.WriteString("}\n\n")

	fmt.Fprintf(&buf, "// lamAlefForms lists the %d Lam-Alef ligatures, sorted by Alef variant.\n", len(lamAlefs))
	buf.WriteString("// Source: UnicodeData-17.0.0.txt (2-codepoint decompositions 0644+<alef>).\n")
	buf.WriteString("var lamAlefForms = [...]lamAlefEntry{\n")
	for _, la := range lamAlefs {
		fmt.Fprintf(&buf,
			"\t{0x%04X, 0x%04X, 0x%04X}, // Lam + U+%04X\n",
			la.alef, la.isolated, la.final, la.alef)
	}
	buf.WriteString("}\n\n")

	// ── lamAlefFormOf lookup ──────────────────────────────────────────────────
	buf.WriteString("// lamAlefFormOf returns the ligature forms for a Lam followed by alef.\n")
	buf.WriteString("// ok is false if alef is not one of the recognised Alef variants.\n")
	buf.WriteString("func lamAlefFormOf(alef rune) (isolated, final rune, ok bool) {\n")
	buf.WriteString("\tfor i := range lamAlefForms {\n")
	buf.WriteString("\t\tif lamAlefForms[i].alef == alef {\n")
	buf.WriteString("\t\t\treturn lamAlefForms[i].isolated, lamAlefForms[i].finalLig, true\n")
	buf.WriteString("\t\t}\n")
	buf.WriteString("\t}\n")
	buf.WriteString("\treturn 0, 0, false\n")
	buf.WriteString("}\n\n")

	// ── missingFormEntry type and table ───────────────────────────────────────
	buf.WriteString("// missingFormEntry records a letter whose Joining_Type is D but that has\n")
	buf.WriteString("// no independent initial/medial Presentation Forms-B glyphs.\n")
	buf.WriteString("// Under the Logical Preservation Contract, when shaped into initial or medial\n")
	buf.WriteString("// position, these letters remain their original logical rune.\n")
	buf.WriteString("type missingFormEntry struct {\n")
	buf.WriteString("\tbase rune\n")
	buf.WriteString("\tnote string\n")
	buf.WriteString("}\n\n")

	fmt.Fprintf(&buf, "// missingForms lists %d letter(s) that are JT=D but lack initial/medial\n", len(missing))
	buf.WriteString("// Presentation Forms-B glyphs. Exposed within internal/rtl for tests and documentation.\n")
	buf.WriteString("var missingForms = [...]missingFormEntry{\n")
	for _, m := range missing {
		fmt.Fprintf(&buf, "\t{0x%04X, %q},\n", m.base, m.note)
	}
	buf.WriteString("}\n\n")

	// ── isMissingForm lookup ──────────────────────────────────────────────────
	buf.WriteString("// isMissingForm reports whether r is a dual-joining letter that lacks\n")
	buf.WriteString("// initial/medial presentation forms.\n")
	buf.WriteString("func isMissingForm(r rune) bool {\n")
	buf.WriteString("\tlo, hi := 0, len(missingForms)-1\n")
	buf.WriteString("\tfor lo <= hi {\n")
	buf.WriteString("\t\tmid := (lo + hi) >> 1\n")
	buf.WriteString("\t\tswitch {\n")
	buf.WriteString("\t\tcase missingForms[mid].base == r:\n")
	buf.WriteString("\t\t\treturn true\n")
	buf.WriteString("\t\tcase missingForms[mid].base < r:\n")
	buf.WriteString("\t\t\tlo = mid + 1\n")
	buf.WriteString("\t\tdefault:\n")
	buf.WriteString("\t\t\thi = mid - 1\n")
	buf.WriteString("\t\t}\n")
	buf.WriteString("\t}\n")
	buf.WriteString("\treturn false\n")
	buf.WriteString("}\n")

	return format.Source(buf.Bytes())
}

// ── Presentation Form string tables ──────────────────────────────────────────

// presMarkRunes is the fixed column order of the transparent combining marks
// used by presForm1MarkStrings. It must match the order of markIndex in
// arabic_shaping.go.
var presMarkRunes = [9]rune{
	0x064B, // Fathatan
	0x064C, // Dammatan
	0x064D, // Kasratan
	0x064E, // Fatha
	0x064F, // Damma
	0x0650, // Kasra
	0x0651, // Shadda
	0x0652, // Sukun
	0x0670, // Dagger Alef
}

// generatePresTables renders internal/rtl/arabic_pres_tables.go: static
// literal string tables for the Arabic Presentation Forms-B block
// (U+FE70..U+FEFF). The content is derived purely from the code points; no
// Unicode data files are read.
func generatePresTables() ([]byte, error) {
	const count = presFormsHi - presFormsLo + 1

	var buf bytes.Buffer
	buf.WriteString("// Code generated by internal/rtl/cmd/genarabic - DO NOT EDIT.\n")
	buf.WriteString("//\n")
	buf.WriteString("// Static literal strings for the Arabic Presentation Forms-B block\n")
	buf.WriteString("// (U+FE70..U+FEFF), emitted as ASCII-only quoted escapes:\n")
	buf.WriteString("//\n")
	buf.WriteString("//\tpresFormStrings[i]         = string(rune(0xFE70 + i))\n")
	buf.WriteString("//\tpresForm1MarkStrings[i][m] = string(rune(0xFE70 + i)) + markRunes[m]\n")
	buf.WriteString("//\n")
	buf.WriteString("// The mark column order matches markIndex in arabic_shaping.go:\n")
	buf.WriteString("//\t064B Fathatan, 064C Dammatan, 064D Kasratan, 064E Fatha,\n")
	buf.WriteString("//\t064F Damma, 0650 Kasra, 0651 Shadda, 0652 Sukun, 0670 Dagger Alef.\n")
	buf.WriteString("//\n")
	buf.WriteString("// Derived purely from code points; no external data files are read.\n")
	buf.WriteString("// Regenerate: go run ./internal/rtl/cmd/genarabic\n")
	buf.WriteString("\n")
	buf.WriteString("package rtl\n\n")

	buf.WriteString("// presFormStrings caches the single-rune string for each Presentation\n")
	buf.WriteString("// Forms-B code point, avoiding string(rune) allocations in the hot path.\n")
	fmt.Fprintf(&buf, "var presFormStrings = [%d]string{\n", count)
	for i := 0; i < count; i++ {
		fmt.Fprintf(&buf, "\t%s,\n", strconv.QuoteToASCII(string(rune(presFormsLo+i))))
	}
	buf.WriteString("}\n\n")

	buf.WriteString("// presForm1MarkStrings caches the base + single-mark string for each\n")
	buf.WriteString("// Presentation Forms-B code point, avoiding slice and string allocations\n")
	buf.WriteString("// on the one-mark fast path. Columns follow markIndex order.\n")
	fmt.Fprintf(&buf, "var presForm1MarkStrings = [%d][%d]string{\n", count, len(presMarkRunes))
	for i := 0; i < count; i++ {
		buf.WriteString("\t{\n")
		for _, m := range presMarkRunes {
			fmt.Fprintf(&buf, "\t\t%s,\n", strconv.QuoteToASCII(string([]rune{rune(presFormsLo + i), m})))
		}
		buf.WriteString("\t},\n")
	}
	buf.WriteString("}\n")

	return format.Source(buf.Bytes())
}
