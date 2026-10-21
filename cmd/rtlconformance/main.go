// Command rtlconformance runs the full Unicode 17.0.0 conformance gates for
// the internal RTL engine. It is intentionally separate from `go test ./...`:
// the corpora hold hundreds of thousands of cases and belong in a dedicated CI
// step.
//
// The two large corpora are stored deterministically gzip-compressed
// (gzip -n -9). The command streams and decompresses them in memory — no
// temporary files — verifies the SHA-256 of the decompressed bytes against the
// official data hashes before any gate runs, and never relies on file names
// alone for content.
//
// Usage:
//
//	go run ./cmd/rtlconformance --unicode internal/rtl/testdata/unicode/17.0.0
//
// The command exits non-zero if any corpus fails verification or any gate
// falls below its recorded threshold:
//
//	BidiCharacterTest: 91707/91707
//	BidiTest:          770241/770241
//	BidiBrackets:      1152/1152
//	Mirroring:         428/428
package main

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"nabd/internal/rtl"
	"nabd/internal/rtl/bidi"
)

const (
	thresholdChar     = 91707
	thresholdBidi     = 770241
	thresholdBrackets = 1152
	thresholdMirrors  = 428

	// maxCodePoint bounds every parsed code point before it is converted to a
	// rune, so the conversion can never truncate.
	maxCodePoint = 0x10FFFF

	// Corpus file names and the official SHA-256 of the decompressed data
	// (see internal/rtl/provenance.md). The gz artifact hashes are recorded
	// there too and are deliberately not used as the data identity.
	corpusCharFile     = "BidiCharacterTest.txt.gz"
	corpusCharSHA      = "a3e6e905ab5afbe318a96df5401d0372a04cd73ef139ab5e3cf0ae241c255488"
	corpusBidiFile     = "BidiTest.txt.gz"
	corpusBidiSHA      = "888bdfc8090652272d1f859cdb00ae659e2dc6c26740be61ef1d03998a687620"
	corpusBracketsFile = "BidiBrackets.txt"
	corpusBracketsSHA  = "dadbaf38a0d0246e5b805bf8725cb81b7c621f93d030595635f5ba2c2f179428"
	corpusMirrorFile   = "BidiMirroring.txt"
	corpusMirrorSHA    = "a2f16fb873ab4fcdf3221cb1a8a85a134ddd6ed03603181823ff5206af3741ce"
)

// Error classes for corpus handling. Tests distinguish them with errors.Is.
var (
	errCorpusOpen = errors.New("rtlconformance: cannot open corpus")
	errCorpusGzip = errors.New("rtlconformance: gzip error")
	errCorpusSHA  = errors.New("rtlconformance: decompressed sha256 mismatch")
	errCorpusRead = errors.New("rtlconformance: read error")
)

// corpusKind distinguishes the storage format of a corpus file.
type corpusKind int

const (
	corpusPlain corpusKind = iota
	corpusGzip
)

// gzipReadCloser streams a gzip file and closes both the decompressor and the
// underlying file on every path.
type gzipReadCloser struct {
	f    *os.File
	z    *gzip.Reader
	path string
}

func (g *gzipReadCloser) Read(p []byte) (int, error) {
	n, err := g.z.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("%w: %s: %v", errCorpusGzip, g.path, err)
	}
	return n, err
}

func (g *gzipReadCloser) Close() error {
	zerr := g.z.Close()
	ferr := g.f.Close()
	if zerr != nil {
		return fmt.Errorf("%w: %s: %v", errCorpusGzip, g.path, zerr)
	}
	return ferr
}

// openCorpus opens a plain-text or gzip-compressed corpus for streaming.
func openCorpus(path string) (io.ReadCloser, corpusKind, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, corpusPlain, fmt.Errorf("%w: %s: %v", errCorpusOpen, path, err)
	}
	if !strings.HasSuffix(path, ".gz") {
		return f, corpusPlain, nil
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, corpusGzip, fmt.Errorf("%w: %s: %v", errCorpusGzip, path, err)
	}
	return &gzipReadCloser{f: f, z: zr, path: path}, corpusGzip, nil
}

// readErr classifies an I/O failure, preserving an already-classified error.
func readErr(path string, err error) error {
	for _, sentinel := range []error{errCorpusOpen, errCorpusGzip, errCorpusSHA, errCorpusRead} {
		if errors.Is(err, sentinel) {
			return err
		}
	}
	return fmt.Errorf("%w: %s: %v", errCorpusRead, path, err)
}

// verifyCorpusSHA streams the corpus through SHA-256 and compares the digest
// of the decompressed bytes with the official data hash.
func verifyCorpusSHA(path, want string) error {
	f, _, err := openCorpus(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return readErr(path, err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("%w: %s: got %s want %s", errCorpusSHA, path, got, want)
	}
	return nil
}

func main() {
	unicodeDir := flag.String("unicode", filepath.Join("internal", "rtl", "testdata", "unicode", "17.0.0"), "directory holding the Unicode data files")
	flag.Parse()

	fail := false
	fmt.Printf("rtlconformance (Unicode data dir: %s)\n\n", *unicodeDir)

	// Verify every corpus before running any gate.
	for _, c := range []struct{ file, sha string }{
		{corpusCharFile, corpusCharSHA},
		{corpusBidiFile, corpusBidiSHA},
		{corpusBracketsFile, corpusBracketsSHA},
		{corpusMirrorFile, corpusMirrorSHA},
	} {
		path := filepath.Join(*unicodeDir, c.file)
		if err := verifyCorpusSHA(path, c.sha); err != nil {
			fmt.Fprintf(os.Stderr, "corpus verification: %v\n", err)
			fail = true
			continue
		}
		fmt.Printf("verified sha256 (decompressed): %s\n", c.file)
	}
	if fail {
		fmt.Println("\nRESULT: FAIL (corpus verification)")
		os.Exit(1)
	}
	fmt.Println()

	n, err := gateBidiCharacterTest(filepath.Join(*unicodeDir, corpusCharFile))
	if err != nil {
		fmt.Fprintf(os.Stderr, "BidiCharacterTest: %v\n", err)
		fail = true
	} else {
		fmt.Printf("BidiCharacterTest: %d/%d\n", n, thresholdChar)
		if n < thresholdChar {
			fmt.Printf("  FAIL: below threshold %d\n", thresholdChar)
			fail = true
		}
	}

	n, err = gateBidiTest(filepath.Join(*unicodeDir, corpusBidiFile))
	if err != nil {
		fmt.Fprintf(os.Stderr, "BidiTest: %v\n", err)
		fail = true
	} else {
		fmt.Printf("BidiTest:          %d/%d\n", n, thresholdBidi)
		if n < thresholdBidi {
			fmt.Printf("  FAIL: below threshold %d\n", thresholdBidi)
			fail = true
		}
	}

	n, err = gateBidiBrackets(filepath.Join(*unicodeDir, corpusBracketsFile))
	if err != nil {
		fmt.Fprintf(os.Stderr, "BidiBrackets: %v\n", err)
		fail = true
	} else {
		fmt.Printf("BidiBrackets:      %d/%d\n", n, thresholdBrackets)
		if n < thresholdBrackets {
			fmt.Printf("  FAIL: below threshold %d\n", thresholdBrackets)
			fail = true
		}
	}

	n, err = gateMirroring(filepath.Join(*unicodeDir, corpusMirrorFile))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Mirroring: %v\n", err)
		fail = true
	} else {
		fmt.Printf("Mirroring:         %d/%d\n", n, thresholdMirrors)
		if n < thresholdMirrors {
			fmt.Printf("  FAIL: below threshold %d\n", thresholdMirrors)
			fail = true
		}
	}

	if fail {
		fmt.Println("\nRESULT: FAIL")
		os.Exit(1)
	}
	fmt.Println("\nRESULT: PASS")
}

// --- BidiCharacterTest ---

func gateBidiCharacterTest(path string) (int, error) {
	f, _, err := openCorpus(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	passed, total := 0, 0
	var failures []string
	lineNo := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		lineNo++
		body := stripComment(sc.Text())
		if body == "" {
			continue
		}
		fields := strings.Split(body, ";")
		if len(fields) != 5 {
			return 0, fmt.Errorf("line %d: expected 5 fields", lineNo)
		}
		var runes []rune
		for _, tok := range strings.Fields(fields[0]) {
			v, err := strconv.ParseUint(tok, 16, 32)
			if err != nil || v > maxCodePoint {
				return 0, fmt.Errorf("line %d: bad code point %q", lineNo, tok)
			}
			runes = append(runes, rune(v))
		}
		dir, err := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err != nil {
			return 0, fmt.Errorf("line %d: bad direction", lineNo)
		}
		wantPara, err := strconv.Atoi(strings.TrimSpace(fields[2]))
		if err != nil {
			return 0, fmt.Errorf("line %d: bad paragraph level", lineNo)
		}
		wantLevels := strings.Fields(fields[3])
		var wantOrder []int
		for _, tok := range strings.Fields(fields[4]) {
			v, err := strconv.Atoi(tok)
			if err != nil {
				return 0, fmt.Errorf("line %d: bad reorder index", lineNo)
			}
			wantOrder = append(wantOrder, v)
		}

		total++
		base := -1
		switch dir {
		case 0:
			base = 0
		case 1:
			base = 1
		}
		a, err := bidi.Analyze(runes, base)
		if err != nil {
			failures = append(failures, fmt.Sprintf("line %d: %v", lineNo, err))
			continue
		}
		ok := int(a.ParaLevel) == wantPara
		ok = ok && levelsMatch(wantLevels, a.Levels)
		ok = ok && orderMatches(wantOrder, filterOrder(a.Order, a.Classes))
		if ok {
			passed++
		} else if len(failures) < 20 {
			failures = append(failures, fmt.Sprintf("line %d: dir=%d wantPara=%d gotPara=%d wantLevels=%v gotLevels=%v wantOrder=%v gotOrder=%v",
				lineNo, dir, wantPara, a.ParaLevel, wantLevels, a.Levels, wantOrder, filterOrder(a.Order, a.Classes)))
		}
	}
	if err := sc.Err(); err != nil {
		return 0, readErr(path, err)
	}
	if passed != total {
		for _, msg := range failures {
			fmt.Println("  ", msg)
		}
		return passed, fmt.Errorf("passed %d of %d", passed, total)
	}
	return passed, nil
}

// --- BidiTest ---

var classRep = map[string]rune{
	"L": 0x0061, "R": 0x05D0, "AL": 0x0627, "EN": 0x0030, "ES": 0x002B,
	"ET": 0x0023, "AN": 0x0660, "CS": 0x002C, "NSM": 0x0300, "BN": 0x00AD,
	"B": 0x2029, "S": 0x0009, "WS": 0x0020, "ON": 0x0021, "LRE": 0x202A,
	"RLE": 0x202B, "PDF": 0x202C, "LRO": 0x202D, "RLO": 0x202E,
	"LRI": 0x2066, "RLI": 0x2067, "FSI": 0x2068, "PDI": 0x2069,
}

func gateBidiTest(path string) (int, error) {
	f, _, err := openCorpus(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	// Validate the representative mapping once.
	for name, r := range classRep {
		class, ok := bidi.ClassFromName(name)
		if !ok {
			return 0, fmt.Errorf("unknown class name %q", name)
		}
		props, _ := bidi.LookupRune(r)
		if props.Class() != class {
			return 0, fmt.Errorf("representative U+%04X for %s has class %s", r, name, bidi.ClassName(props.Class()))
		}
	}

	passed, total := 0, 0
	var failures []string
	var wantLevels []string
	var wantOrder []int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		body := stripComment(sc.Text())
		if body == "" {
			continue
		}
		if strings.HasPrefix(body, "@") {
			switch {
			case strings.HasPrefix(body, "@Levels:"):
				wantLevels = strings.Fields(strings.TrimSpace(strings.TrimPrefix(body, "@Levels:")))
			case strings.HasPrefix(body, "@Reorder:"):
				wantOrder = nil
				for _, tok := range strings.Fields(strings.TrimSpace(strings.TrimPrefix(body, "@Reorder:"))) {
					v, err := strconv.Atoi(tok)
					if err != nil {
						return 0, fmt.Errorf("line %d: bad reorder", lineNo)
					}
					wantOrder = append(wantOrder, v)
				}
			}
			continue
		}
		fields := strings.Split(body, ";")
		if len(fields) != 2 {
			return 0, fmt.Errorf("line %d: expected 2 fields", lineNo)
		}
		tokens := strings.Fields(fields[0])
		bits, err := strconv.ParseUint(strings.TrimSpace(fields[1]), 16, 8)
		if err != nil || bits > 7 {
			return 0, fmt.Errorf("line %d: bad bitset", lineNo)
		}
		var runes []rune
		for _, tok := range tokens {
			r, ok := classRep[tok]
			if !ok {
				return 0, fmt.Errorf("line %d: unknown class %q", lineNo, tok)
			}
			runes = append(runes, r)
		}
		for _, bit := range []int{1, 2, 4} {
			if int(bits)&bit == 0 {
				continue
			}
			base := -1
			switch bit {
			case 2:
				base = 0
			case 4:
				base = 1
			}
			total++
			a, err := bidi.Analyze(runes, base)
			if err != nil {
				failures = append(failures, fmt.Sprintf("line %d: %v", lineNo, err))
				continue
			}
			ok := levelsMatch(wantLevels, a.Levels) &&
				orderMatches(wantOrder, filterOrder(a.Order, a.Classes))
			if ok {
				passed++
			} else if len(failures) < 20 {
				failures = append(failures, fmt.Sprintf("line %d bit %d: %s: wantLevels=%v gotLevels=%v wantOrder=%v gotOrder=%v",
					lineNo, bit, fields[0], wantLevels, a.Levels, wantOrder, filterOrder(a.Order, a.Classes)))
			}
		}
	}
	if err := sc.Err(); err != nil {
		return 0, readErr(path, err)
	}
	if passed != total {
		for _, msg := range failures {
			fmt.Println("  ", msg)
		}
		return passed, fmt.Errorf("passed %d of %d", passed, total)
	}
	return passed, nil
}

// --- BidiBrackets ---

type bracketPair struct {
	opener rune
	closer rune
}

func gateBidiBrackets(path string) (int, error) {
	pairs, err := parseBracketPairs(path)
	if err != nil {
		return 0, err
	}

	passed := 0
	// Structural checks: kinds and unified pair identifiers.
	for _, p := range pairs {
		if bidi.BracketKind(p.opener) != 1 || bidi.BracketKind(p.closer) != 2 {
			return passed, fmt.Errorf("pair U+%04X/U+%04X: wrong bracket kinds", p.opener, p.closer)
		}
		if bidi.BracketPairID(p.opener) != bidi.BracketPairID(p.closer) {
			return passed, fmt.Errorf("pair U+%04X/U+%04X: identifiers differ", p.opener, p.closer)
		}
		passed += 2
	}

	// Contextual checks: every pair resolves, reorders, mirrors and maps
	// correctly in eight contexts under auto and forced-RTL paragraphs.
	// Arabic-class runes are built programmatically so this file carries no
	// literal Arabic text (language guard #223): U+0662 is an Arabic-Indic
	// digit (AN) and U+064B is a fatha (NSM).
	arabicDigitTwo := string(rune(0x0662))
	fatha := string(rune(0x064B))
	contexts := []struct {
		name    string
		text    func(o, c rune) string
		matched bool
	}{
		{"rtl_prose_ltr_inside", func(o, c rune) string { return "אבג " + string(o) + " abc " + string(c) + " דה" }, true},
		{"ltr_prose_rtl_inside", func(o, c rune) string { return "abc " + string(o) + " אבג " + string(c) + " def" }, true},
		{"nested", func(o, c rune) string { return "אב " + string(o) + string(o) + "x" + string(c) + string(c) + " גד" }, true},
		{"adjacent", func(o, c rune) string {
			return "אב " + string(o) + "a" + string(c) + string(o) + "b" + string(c) + " גד"
		}, true},
		{"unmatched_open", func(o, c rune) string { return "אב " + string(o) + "x גד" }, false},
		{"unmatched_close", func(o, c rune) string { return "אב x" + string(c) + " גד" }, false},
		{"mixed_digits", func(o, c rune) string {
			return "אב " + string(o) + "1 " + arabicDigitTwo + " 3" + string(c) + " גד"
		}, true},
		{"combining_marks", func(o, c rune) string { return "אב " + string(o) + "a\u0301 אבג" + fatha + string(c) + " גד" }, true},
	}
	for _, p := range pairs {
		for _, ctx := range contexts {
			text := []rune(ctx.text(p.opener, p.closer))
			for _, base := range []int{-1, 1} {
				a, err := bidi.Analyze(text, base)
				if err != nil {
					return passed, fmt.Errorf("pair U+%04X/%U ctx %s: %v", p.opener, p.closer, ctx.name, err)
				}
				if len(a.Levels) != len(text) || len(a.Order) != len(text) {
					return passed, fmt.Errorf("pair U+%04X ctx %s: rune loss", p.opener, ctx.name)
				}
				seen := make([]bool, len(text))
				for _, idx := range a.Order {
					if idx < 0 || idx >= len(text) || seen[idx] {
						return passed, fmt.Errorf("pair U+%04X ctx %s: order is not a permutation", p.opener, ctx.name)
					}
					seen[idx] = true
				}
				for i, l := range a.Levels {
					if l > 125 {
						return passed, fmt.Errorf("pair U+%04X ctx %s: level %d at %d", p.opener, ctx.name, l, i)
					}
				}
				if ctx.matched {
					io := indexRune(text, p.opener)
					ic := indexRune(text, p.closer)
					if io < 0 || ic < 0 || a.Levels[io] != a.Levels[ic] {
						return passed, fmt.Errorf("pair U+%04X/U+%04X ctx %s base %d: pair levels differ",
							p.opener, p.closer, ctx.name, base)
					}
					visual := distinctMirrorText(text, a)
					if msg := mirrorExpectation(visual, p.opener, p.closer, a.Levels[io]); msg != "" {
						return passed, fmt.Errorf("pair U+%04X/U+%04X ctx %s base %d: %s",
							p.opener, p.closer, ctx.name, base, msg)
					}
				}
				passed++
			}
		}
	}
	return passed, nil
}

func parseBracketPairs(path string) ([]bracketPair, error) {
	f, _, err := openCorpus(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	pairs := map[rune]rune{}
	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		body := stripComment(sc.Text())
		if body == "" {
			continue
		}
		fields := strings.Split(body, ";")
		if len(fields) != 3 {
			return nil, fmt.Errorf("line %d: expected 3 fields", lineNo)
		}
		cp, err1 := strconv.ParseUint(strings.TrimSpace(fields[0]), 16, 32)
		pair, err2 := strconv.ParseUint(strings.TrimSpace(fields[1]), 16, 32)
		kind := strings.TrimSpace(fields[2])
		if err1 != nil || err2 != nil || cp > maxCodePoint || pair > maxCodePoint || (kind != "o" && kind != "c") {
			return nil, fmt.Errorf("line %d: bad bracket entry", lineNo)
		}
		if kind == "o" {
			pairs[rune(cp)] = rune(pair)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, readErr(path, err)
	}
	var out []bracketPair
	var openers []rune
	for o := range pairs {
		openers = append(openers, o)
	}
	sortRunes(openers)
	for _, o := range openers {
		out = append(out, bracketPair{opener: o, closer: pairs[o]})
	}
	return out, nil
}

func sortRunes(rs []rune) {
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && rs[j-1] > rs[j]; j-- {
			rs[j-1], rs[j] = rs[j], rs[j-1]
		}
	}
}

// distinctMirrorText builds the L4 visual string (odd levels mirrored).
func distinctMirrorText(text []rune, a bidi.Analysis) string {
	var b strings.Builder
	for _, idx := range a.Order {
		r := text[idx]
		if a.Levels[idx]%2 == 1 {
			b.WriteRune(rtl.Mirror(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func mirrorExpectation(visual string, o, c rune, level uint8) string {
	contains := func(r rune) bool { return strings.ContainsRune(visual, r) }
	if level%2 == 1 {
		mo, mc := rtl.Mirror(o), rtl.Mirror(c)
		if mo != o && !contains(mo) {
			return fmt.Sprintf("odd level %d: mirror of U+%04X (U+%04X) missing", level, o, mo)
		}
		if mc != c && !contains(mc) {
			return fmt.Sprintf("odd level %d: mirror of U+%04X (U+%04X) missing", level, c, mc)
		}
		return ""
	}
	if !contains(o) || !contains(c) {
		return fmt.Sprintf("even level %d: bracket missing unmirrored", level)
	}
	return ""
}

func indexRune(text []rune, r rune) int {
	for i, x := range text {
		if x == r {
			return i
		}
	}
	return -1
}

// --- Mirroring ---

func gateMirroring(path string) (int, error) {
	f, _, err := openCorpus(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	passed, total := 0, 0
	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		body := stripComment(sc.Text())
		if body == "" {
			continue
		}
		fields := strings.Split(body, ";")
		if len(fields) != 2 {
			return 0, fmt.Errorf("line %d: expected 2 fields", lineNo)
		}
		cp, err1 := strconv.ParseUint(strings.TrimSpace(fields[0]), 16, 32)
		mr, err2 := strconv.ParseUint(strings.TrimSpace(fields[1]), 16, 32)
		if err1 != nil || err2 != nil || cp > maxCodePoint || mr > maxCodePoint {
			return 0, fmt.Errorf("line %d: bad entry", lineNo)
		}
		total++
		if rtl.Mirror(rune(cp)) == rune(mr) {
			passed++
		}
	}
	if err := sc.Err(); err != nil {
		return 0, readErr(path, err)
	}
	if passed != total {
		return passed, fmt.Errorf("mirror table mismatch: %d of %d entries differ", total-passed, total)
	}
	if total < thresholdMirrors {
		return passed, fmt.Errorf("data file has only %d entries", total)
	}
	return passed, nil
}

// --- helpers ---

func stripComment(line string) string {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSpace(line)
}

func levelsMatch(want []string, got []uint8) bool {
	if len(want) != len(got) {
		return false
	}
	for i, w := range want {
		if w == "x" {
			continue
		}
		v, err := strconv.Atoi(w)
		if err != nil || int(got[i]) != v {
			return false
		}
	}
	return true
}

func orderMatches(want, got []int) bool {
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i] != got[i] {
			return false
		}
	}
	return true
}

// filterOrder drops rule-X9-removed runes from a visual order.
func filterOrder(order []int, classes []bidi.Class) []int {
	out := make([]int, 0, len(order))
	for _, idx := range order {
		if idx >= 0 && idx < len(classes) && bidi.IsRemovedByX9(classes[idx]) {
			continue
		}
		out = append(out, idx)
	}
	return out
}
