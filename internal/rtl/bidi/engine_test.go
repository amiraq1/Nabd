package bidi

import (
	"reflect"
	"testing"
)

func runes(hex ...rune) []rune { return hex }

func TestAnalyzeNCCanonicalPairs(t *testing.T) {
	cases := []struct {
		name   string
		input  []rune
		base   int
		levels []uint8
		order  []int
	}{
		{
			// BidiCharacterTest-17.0.0 line 314
			name:   "NC1 line314",
			input:  []rune{0x0061, 0x0020, 0x2329, 0x0062, 0x002E, 0x0031, 0x3009},
			base:   1,
			levels: []uint8{2, 2, 2, 2, 2, 2, 2},
			order:  []int{0, 1, 2, 3, 4, 5, 6},
		},
		{
			// BidiCharacterTest-17.0.0 line 315
			name:   "NC2 line315",
			input:  []rune{0x0061, 0x0020, 0x3008, 0x0062, 0x002E, 0x0031, 0x232A},
			base:   1,
			levels: []uint8{2, 2, 2, 2, 2, 2, 2},
			order:  []int{0, 1, 2, 3, 4, 5, 6},
		},
		{
			// BidiCharacterTest-17.0.0 line 318
			name:   "NC3 line318",
			input:  []rune{0x05D0, 0x0020, 0x2329, 0x05D1, 0x002E, 0x0031, 0x3009},
			base:   0,
			levels: []uint8{1, 1, 1, 1, 1, 2, 1},
			order:  []int{6, 5, 4, 3, 2, 1, 0},
		},
		{
			// BidiCharacterTest-17.0.0 line 319
			name:   "NC4 line319",
			input:  []rune{0x05D0, 0x0020, 0x3008, 0x05D1, 0x002E, 0x0031, 0x232A},
			base:   0,
			levels: []uint8{1, 1, 1, 1, 1, 2, 1},
			order:  []int{6, 5, 4, 3, 2, 1, 0},
		},
	}
	for _, tc := range cases {
		a, err := Analyze(tc.input, tc.base)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !reflect.DeepEqual(a.Levels, tc.levels) {
			t.Errorf("%s: levels = %v, want %v", tc.name, a.Levels, tc.levels)
		}
		if !reflect.DeepEqual(a.Order, tc.order) {
			t.Errorf("%s: order = %v, want %v", tc.name, a.Order, tc.order)
		}
	}
}

func TestAnalyzeUAXBracketExample(t *testing.T) {
	// BidiCharacterTest-17.0.0 line 45 (examples from UAX #9).
	input := []rune{0x05D0, 0x05D1, 0x0028, 0x05D2, 0x05D3, 0x005B, 0x0026, 0x0065, 0x0066, 0x005D, 0x002E, 0x0029, 0x0067, 0x0068}
	wantLevels := []uint8{1, 1, 1, 1, 1, 1, 1, 2, 2, 1, 1, 1, 2, 2}
	wantOrder := []int{12, 13, 11, 10, 9, 7, 8, 6, 5, 4, 3, 2, 1, 0}
	a, err := Analyze(input, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Levels, wantLevels) {
		t.Errorf("levels = %v, want %v", a.Levels, wantLevels)
	}
	if !reflect.DeepEqual(a.Order, wantOrder) {
		t.Errorf("order = %v, want %v", a.Order, wantOrder)
	}
}

func TestBracketPairIDsUnify(t *testing.T) {
	pairs := [][2]rune{
		{'(', ')'}, {'[', ']'}, {'{', '}'},
		{0x2329, 0x3009}, // canonical equivalence per BidiBrackets.txt
		{0x3008, 0x232A},
	}
	for _, p := range pairs {
		if got, want := BracketKind(p[0]), uint8(1); got != want {
			t.Errorf("BracketKind(%U) = %d, want %d", p[0], got, want)
		}
		if got, want := BracketKind(p[1]), uint8(2); got != want {
			t.Errorf("BracketKind(%U) = %d, want %d", p[1], got, want)
		}
		if BracketPairID(p[0]) != BracketPairID(p[1]) {
			t.Errorf("pair %U/%U ids differ: %U vs %U", p[0], p[1], BracketPairID(p[0]), BracketPairID(p[1]))
		}
	}
	// canonical pairs share identifiers across the equivalence class
	if BracketPairID(0x2329) != BracketPairID(0x232A) {
		t.Errorf("U+2329 and U+232A must unify: %U vs %U", BracketPairID(0x2329), BracketPairID(0x232A))
	}
	// Angle brackets are mirrored (rule L4) but are not paired brackets.
	if BracketKind('<') != 0 || BracketKind('>') != 0 {
		t.Errorf("< and > must not be reported as paired brackets")
	}
	if BracketKind('a') != 0 {
		t.Errorf("non-bracket reported as bracket")
	}
}

func TestReverseBracketCanonical(t *testing.T) {
	if got := ReverseBracket('('); got != ')' {
		t.Errorf("ReverseBracket('(') = %U", got)
	}
	if got := ReverseBracket(0x2329); got != 0x3009 {
		t.Errorf("ReverseBracket(U+2329) = %U, want U+3009", got)
	}
	if got := ReverseBracket(0x3009); got != 0x3008 {
		t.Errorf("ReverseBracket(U+3009) = %U, want U+3008", got)
	}
}

func TestAnalyzeClassesAndBase(t *testing.T) {
	// "R R" forced RTL: level 1, reversed order.
	a, err := AnalyzeClasses([]Class{R, R}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Levels, []uint8{1, 1}) || !reflect.DeepEqual(a.Order, []int{1, 0}) {
		t.Errorf("R R base1: levels=%v order=%v", a.Levels, a.Order)
	}
	// "L" forced RTL: level 2.
	a, err = AnalyzeClasses([]Class{L}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Levels, []uint8{2}) {
		t.Errorf("L base1: levels=%v", a.Levels)
	}
	if _, err := Analyze(runes('a'), 2); err == nil {
		t.Error("invalid base must error")
	}
	if a, _ := Analyze(runes('a'), -1); a.ParaLevel != 0 {
		t.Errorf("auto LTR para level = %d", a.ParaLevel)
	}
	if a, _ := Analyze(runes(0x05D0), -1); a.ParaLevel != 1 {
		t.Errorf("auto RTL para level = %d", a.ParaLevel)
	}
}

func TestClassNames(t *testing.T) {
	for _, name := range []string{"L", "R", "AL", "EN", "NSM", "BN", "B", "S", "WS", "ON", "LRI", "PDI"} {
		c, ok := ClassFromName(name)
		if !ok {
			t.Fatalf("ClassFromName(%q) failed", name)
		}
		if ClassName(c) != name {
			t.Errorf("round trip %q -> %q", name, ClassName(c))
		}
	}
	if _, ok := ClassFromName("ZZZ"); ok {
		t.Error("unknown class accepted")
	}
}
