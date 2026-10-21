package rtl

import "testing"

// TestPresFormTablesDerivation verifies that every entry of presFormStrings
// and presForm1MarkStrings derives exactly from its Presentation Forms-B code
// point: 144 single-rune entries plus 144x9 base+mark entries (1,440 total).
//
// The mark column order must match markIndex (arabic_shaping.go); this test
// couples the two so a change in one without the other fails.
func TestPresFormTablesDerivation(t *testing.T) {
	const lo, hi = 0xFE70, 0xFEFF
	wantCount := int(hi-lo) + 1
	if len(presFormStrings) != wantCount {
		t.Fatalf("presFormStrings has %d entries, want %d", len(presFormStrings), wantCount)
	}
	if len(presForm1MarkStrings) != wantCount {
		t.Fatalf("presForm1MarkStrings has %d rows, want %d", len(presForm1MarkStrings), wantCount)
	}

	marks := [9]rune{0x064B, 0x064C, 0x064D, 0x064E, 0x064F, 0x0650, 0x0651, 0x0652, 0x0670}
	for mi, m := range marks {
		if got := markIndex(m); got != mi {
			t.Fatalf("markIndex(0x%04X) = %d, want column %d", m, got, mi)
		}
	}

	checked := 0
	for i := 0; i < wantCount; i++ {
		r := rune(lo + i)
		if want := string([]rune{r}); presFormStrings[i] != want {
			t.Errorf("presFormStrings[%d] = %q, want %q", i, presFormStrings[i], want)
		}
		checked++
		if got := len(presForm1MarkStrings[i]); got != len(marks) {
			t.Fatalf("presForm1MarkStrings[%d] has %d columns, want %d", i, got, len(marks))
		}
		for mi, m := range marks {
			if want := string([]rune{r, m}); presForm1MarkStrings[i][mi] != want {
				t.Errorf("presForm1MarkStrings[%d][%d] = %q, want %q (base U+%04X + U+%04X)",
					i, mi, presForm1MarkStrings[i][mi], want, r, m)
			}
			checked++
		}
	}
	if checked != 1440 {
		t.Fatalf("checked %d entries, want 1440", checked)
	}
}
