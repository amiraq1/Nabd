package bidi

import (
	"reflect"
	"testing"
)

func TestWeakAndNeutralRules(t *testing.T) {
	cases := []struct {
		name       string
		classes    []Class
		base       int
		wantLevels []uint8
		wantOrder  []int
	}{
		{
			// Rule W5: A sequence of European terminators (ET) adjacent to a
			// European number (EN) is rewritten to EN. In an RTL paragraph (base 1),
			// EN resolves to level 2 while surrounding R runes resolve to level 1.
			name:       "W5_adjacent_ET_becomes_EN",
			classes:    []Class{R, ET, ET, EN, R},
			base:       1,
			wantLevels: []uint8{1, 2, 2, 2, 1},
			wantOrder:  []int{4, 1, 2, 3, 0},
		},
		{
			// Rules W5+W6+N1: An ET sequence without an adjacent EN is not rewritten to EN.
			// This test exercises W5+W6+N1 together, not W5 alone: Rule W6 changes ET
			// to ON, and Rule N1 resolves ON between R runes to R.
			// All runes resolve to level 1 (unlike the W5 rewrite to level 2).
			name:       "W5_isolated_ET_not_rewritten_to_EN",
			classes:    []Class{R, ET, ET, R},
			base:       1,
			wantLevels: []uint8{1, 1, 1, 1},
			wantOrder:  []int{3, 2, 1, 0},
		},
		{
			// Rules N1 and N2:
			// - N1: The first ON between R and R resolves to R (level 1 in LTR).
			// - N2: The second ON between R and L resolves to paragraph embedding direction (L, level 0).
			name:       "N1_same_direction_and_N2_embedding_direction",
			classes:    []Class{R, ON, R, ON, L},
			base:       0,
			wantLevels: []uint8{1, 1, 1, 0, 0},
			wantOrder:  []int{2, 1, 0, 3, 4},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := AnalyzeClasses(tc.classes, tc.base)
			if err != nil {
				t.Fatalf("AnalyzeClasses failed: %v", err)
			}
			if !reflect.DeepEqual(a.Levels, tc.wantLevels) {
				t.Errorf("levels = %v, want %v", a.Levels, tc.wantLevels)
			}
			if !reflect.DeepEqual(a.Order, tc.wantOrder) {
				t.Errorf("order = %v, want %v", a.Order, tc.wantOrder)
			}
		})
	}
}
