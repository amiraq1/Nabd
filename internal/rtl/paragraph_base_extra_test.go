package rtl

// paragraph_base_extra_test.go — الاختبارات الأربعة الإضافية المطلوبة
//
// 1. تكافؤ السطر الواحد: AnalyzeWithLineBreaks([len(runes)]) == Analyze
// 2. فقرتان مستقلتان: كل فقرة تحسم Auto بصورة مستقلة عبر \n
// 3. فهارس rune مقابل grapheme clusters: combining marks و ZWJ قبل حد السطر
// 4. Path span داخل فقرة RTL ملتفة: ذري، مستوى زوجي، لا دمج، overflow صحيح

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"nabd/internal/rtl/bidi"
)

// ── 1. TestAnalyzeWithLineBreaksSingleLineParity ──────────────────────────────
//
// AnalyzeWithLineBreaks(text, base, []int{len([]rune(text))}) يجب أن يساوي
// Analyze(text, base) في ParaLevel وLevels وOrder (لا يوجد L1 reset إضافي
// لأنه سطر واحد كامل).
//
// OBSERVED: يختبر المسار الداخلي AnalyzeWithLineBreaks بشكل مستقل عن Layout.
func TestAnalyzeWithLineBreaksSingleLineParity(t *testing.T) {
	// حالات ثابتة (لا نص فارغ — bidi.Analyze ترفضه بـ "types is null")
	fixed := []struct {
		name string
		text string
		base int
	}{
		{"rtl_auto", "\u05D0\u05D1\u05D2 abc", -1},
		{"ltr_auto", "hello \u0645\u0631\u062d\u0628\u0627", -1},
		{"rtl_forced", "\u0627\u0628\u062C book", 1},
		{"ltr_forced", "\u05D0\u05D1 go test", 0},
		{"brackets", "\u05D0\u05D1 (text) \u05D2\u05D3", -1},
		{"numbers", "\u0661\u0662\u0663 abc 456", -1},
		{"ascii_only", "hello world", -1},
		{"single_rtl", "\u05D0", -1},
		{"single_ltr", "a", -1},
	}

	for _, tc := range fixed {
		t.Run(tc.name, func(t *testing.T) {
			runes := []rune(tc.text)
			n := len(runes)

			wantA, err := bidi.Analyze(runes, tc.base)
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}

			gotA, gotPieces, err := bidi.AnalyzeWithLineBreaks(runes, tc.base, []int{n})
			if err != nil {
				t.Fatalf("AnalyzeWithLineBreaks: %v", err)
			}

			if gotA.ParaLevel != wantA.ParaLevel {
				t.Errorf("ParaLevel: got %d, want %d", gotA.ParaLevel, wantA.ParaLevel)
			}
			if len(gotPieces) != 1 {
				t.Fatalf("expected 1 piece, got %d", len(gotPieces))
			}
			if len(gotPieces[0]) != len(wantA.Levels) {
				t.Fatalf("levels len: got %d, want %d", len(gotPieces[0]), len(wantA.Levels))
			}
			for i := range wantA.Levels {
				if gotPieces[0][i] != wantA.Levels[i] {
					t.Errorf("level[%d]: got %d, want %d (rune %q)",
						i, gotPieces[0][i], wantA.Levels[i], runes[i])
				}
			}
			if len(gotA.Order) != len(wantA.Order) {
				t.Fatalf("order len: got %d, want %d", len(gotA.Order), len(wantA.Order))
			}
			for i := range wantA.Order {
				if gotA.Order[i] != wantA.Order[i] {
					t.Errorf("order[%d]: got %d, want %d", i, gotA.Order[i], wantA.Order[i])
				}
			}
		})
	}

	// عينة عشوائية ببذرة ثابتة.
	t.Run("random_seed42", func(t *testing.T) {
		rng := rand.New(rand.NewSource(42))
		// مجموعة الرونات المحتملة: عبري، عربي، لاتيني، أرقام، فراغات، أقواس
		pool := []rune{
			'a', 'b', 'c', 'x', 'y', ' ',
			'\u05D0', '\u05D1', '\u05D2', // עבריت
			'\u0627', '\u0628', '\u062C', // عربي
			'(', ')', '[', ']', '1', '2',
		}
		for i := 0; i < 50; i++ {
			n := rng.Intn(15) + 1
			runes := make([]rune, n)
			for j := range runes {
				runes[j] = pool[rng.Intn(len(pool))]
			}
			for _, base := range []int{-1, 0, 1} {
				wantA, err := bidi.Analyze(runes, base)
				if err != nil {
					t.Fatalf("Analyze: %v", err)
				}
				gotA, pieces, err := bidi.AnalyzeWithLineBreaks(runes, base, []int{len(runes)})
				if err != nil {
					t.Fatalf("AnalyzeWithLineBreaks: %v", err)
				}
				if gotA.ParaLevel != wantA.ParaLevel {
					t.Errorf("seed42 i=%d base=%d: ParaLevel got %d want %d (text=%q)",
						i, base, gotA.ParaLevel, wantA.ParaLevel, string(runes))
				}
				for j := range wantA.Levels {
					if pieces[0][j] != wantA.Levels[j] {
						t.Errorf("seed42 i=%d base=%d j=%d: level got %d want %d (text=%q)",
							i, base, j, pieces[0][j], wantA.Levels[j], string(runes))
						break
					}
				}
				for j := range wantA.Order {
					if gotA.Order[j] != wantA.Order[j] {
						t.Errorf("seed42 i=%d base=%d j=%d: order got %d want %d (text=%q)",
							i, base, j, gotA.Order[j], wantA.Order[j], string(runes))
						break
					}
				}
			}
		}
	})
}

// ── 2. TestTwoIndependentParagraphs ──────────────────────────────────────────
//
// فقرة عربية ثم \n ثم فقرة لاتينية. كل فقرة تحسم Auto بصورة مستقلة.
// ParaLevel العربي لا يمتد إلى الفقرة اللاتينية.
//
// OBSERVED: Layout يُقسّم عند \n ويُحلّل كل فقرة منفصلة.
func TestTwoIndependentParagraphs(t *testing.T) {
	// الفقرة الأولى: عربية → RTL (paraLevel=1)
	// الفقرة الثانية: لاتينية → LTR (paraLevel=0)
	text := "\u0645\u0631\u062d\u0628\u0627\nhello world"

	lines := render(t, text, 120, Policy{Mode: ReorderAndMirror, Base: Auto})
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (one per paragraph), got %d", len(lines))
	}

	// السطر الأول عربي — يجب أن يكون RTL (مستوى فردي)
	arabicRTL := false
	for _, r := range lines[0].Runs {
		if r.Kind == Prose && r.Level%2 == 1 {
			arabicRTL = true
		}
	}
	if !arabicRTL {
		t.Errorf("first paragraph: expected RTL prose run, levels = %v",
			collectLevels(lines[0]))
	}

	// السطر الثاني لاتيني — يجب أن يكون LTR (مستوى زوجي)
	latinLTR := true
	for _, r := range lines[1].Runs {
		if r.Kind == Prose && r.Level%2 == 1 {
			latinLTR = false
		}
	}
	if !latinLTR {
		t.Errorf("second paragraph: expected LTR prose run, levels = %v",
			collectLevels(lines[1]))
	}

	// الترتيب البصري: السطر العربي يعكس الترتيب
	arabicVisual := visualOf([]VisualLine{lines[0]})
	// "مرحبا" مُعاد ترتيبه
	if !strings.Contains(arabicVisual, "\u0627\u0628\u062d\u0631\u0645") {
		t.Errorf("arabic paragraph visual = %q — expected reversed order", arabicVisual)
	}

	// السطر اللاتيني يبقى كما هو
	latinVisual := visualOf([]VisualLine{lines[1]})
	if latinVisual != "hello world" {
		t.Errorf("latin paragraph visual = %q, want %q", latinVisual, "hello world")
	}
}

func collectLevels(l VisualLine) []uint8 {
	var out []uint8
	for _, r := range l.Runs {
		for range r.Clusters {
			out = append(out, r.Level)
		}
	}
	return out
}

// ── 3. TestRuneIndexVsGraphemeClusters ───────────────────────────────────────
//
// نص متعدد البايتات مع combining marks وZWJ emoji قبل حد السطر.
// يتحقق من: لا انقسام عنقود، لا إزاحة خاطئة في linebreaks.
//
// OBSERVED: إذا كانت AnalyzeWithLineBreaks تحسب linebreaks بفهارس rune
// صحيحة، تظل العناقيد سليمة. أي خطأ في التحسب ينتج IndexOutOfRange أو
// مستويات خاطئة.
func TestRuneIndexVsGraphemeClusters(t *testing.T) {
	cases := []struct {
		name string
		text string
		w    int
		base Direction
	}{
		{
			// e + combining acute (2 bytes) + euro sign (3 bytes)، ثم عبري
			name: "combining_mark_at_boundary",
			text: "e\u0301\u20AC \u05D0\u05D1",
			w:    2,
			base: Auto,
		},
		{
			// عربي مع harakāt (combining marks) + لاتيني
			name: "arabic_with_marks",
			text: "\u0645\u064E\u0631\u0652\u062d\u064E\u0628\u064B\u0627 go",
			w:    5,
			base: Auto,
		},
		{
			// ZWJ emoji قبل حد السطر المحتمل
			name: "zwj_emoji_before_boundary",
			text: "ab \U0001F468\u200D\U0001F469\u200D\U0001F467\u200D\U0001F466 cd",
			w:    3,
			base: Auto,
		},
		{
			// lam-alef: grapheme يتكون من rune-ين
			name: "lam_alef",
			text: "\u0644\u064E\u0627 \u05D0\u05D1",
			w:    2,
			base: RTL,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := render(t, tc.text, tc.w, Policy{Mode: ReorderAndMirror, Base: tc.base})

			// لا عنقود يبدأ بـ combining mark.
			for li, l := range lines {
				for _, r := range l.Runs {
					for _, c := range r.Clusters {
						if len(c.Text) == 0 {
							t.Errorf("line %d: empty cluster", li)
							continue
						}
						firstRune, _ := utf8.DecodeRuneInString(c.Text)
						if isMarkRune(firstRune) {
							t.Errorf("line %d cluster %q starts with combining mark U+%04X",
								li, c.Text, firstRune)
						}
					}
				}
			}

			// الاستعادة من source map تُرجع النص الأصلي.
			restored, err := RestoreFromSource(tc.text, clustersOf(lines))
			if err != nil {
				t.Fatalf("RestoreFromSource: %v", err)
			}
			if restored != tc.text {
				t.Errorf("restore: got %q, want %q", restored, tc.text)
			}
		})
	}

	// ZWJ emoji يبقى عنقوداً واحداً بعرض 2.
	t.Run("zwj_stays_one_cluster", func(t *testing.T) {
		text := "ab \U0001F468\u200D\U0001F469\u200D\U0001F467\u200D\U0001F466 cd"
		lines := render(t, text, 120, Policy{Mode: ReorderAndMirror, Base: Auto})
		found := false
		for _, c := range clustersOf(lines) {
			if strings.Contains(c.Text, "\U0001F468") {
				found = true
				want := "\U0001F468\u200D\U0001F469\u200D\U0001F467\u200D\U0001F466"
				if c.Text != want || c.Width != 2 {
					t.Errorf("emoji cluster = %q width %d, want %q width 2", c.Text, c.Width, want)
				}
			}
		}
		if !found {
			t.Error("ZWJ family emoji cluster not found")
		}
	})
}

// ── 4. TestPathSpanInWrappedRTLParagraph ─────────────────────────────────────
//
// Path span داخل فقرة RTL ملتفة:
//   - يبقى ذريًا (لا ينقسم عند عرض ≥ len(path))
//   - يبقى بمستوى زوجي (LTR island)
//   - لا يُدمج مع span مجاور
//   - سياسة overflow الطويلة تبقى كما هي (تنقسم عند حدود عنقود فقط)
//
// OBSERVED: يكمّل TestPathSpanAtomicAcrossWidths بإضافة السياق العربي
// والتحقق من المستوى الزوجي والدمج.
func TestPathSpanInWrappedRTLParagraph(t *testing.T) {
	const pathStr = "internal/ui/feed.go"
	// فقرة RTL: عربي، مسار، عربي
	text := "\u0645\u0631\u062d\u0628\u0627 " + pathStr + " \u0627\u0628\u062c"
	pathStart := strings.Index(text, pathStr)
	if pathStart < 0 {
		t.Fatal("path not found")
	}
	pathSpan := Span{Start: pathStart, End: pathStart + len(pathStr), Kind: Path, StyleID: 7}

	// ── أ) الذرية: عند عرض ≥ len(path)، المسار على سطر واحد ──
	for w := len(pathStr); w <= len(text); w++ {
		lines := renderSpans(t, text, []Span{pathSpan}, w,
			Policy{Mode: ReorderAndMirror, Base: Auto})
		found := false
		for _, l := range lines {
			v := visualOf([]VisualLine{l})
			if strings.Contains(v, pathStr) {
				found = true
			}
			// لا سطر يحتوي جزءاً من المسار دون المسار كاملاً
			for i := 1; i < len(pathStr); i++ {
				pfx := pathStr[:i]
				if strings.Contains(v, pfx) && !strings.Contains(v, pathStr) {
					t.Errorf("w=%d: path split — line has prefix %q but not full path:\n  line=%q",
						w, pfx, v)
					break
				}
			}
		}
		if !found {
			t.Errorf("w=%d: path not found in any line", w)
		}
	}

	// ── ب) المستوى الزوجي: كل cluster في span المسار له مستوى زوجي ──
	lines := renderSpans(t, text, []Span{pathSpan}, 120,
		Policy{Mode: ReorderAndMirror, Base: Auto})
	for _, l := range lines {
		for _, r := range l.Runs {
			if r.Kind != Path {
				continue
			}
			if r.Level%2 != 0 {
				t.Errorf("path span run has odd level %d (expected even LTR island)", r.Level)
			}
		}
	}

	// ── ج) لا دمج مع prose مجاور ──
	for _, l := range lines {
		for _, r := range l.Runs {
			if r.Kind != Path {
				continue
			}
			// StyleID يجب أن يكون 7 (من الـ span)
			if r.StyleID != 7 {
				t.Errorf("path run StyleID = %d, want 7", r.StyleID)
			}
		}
	}
	// يجب أن يوجد run واحد على الأقل بـ Kind==Prose
	proseFound := false
	for _, l := range lines {
		for _, r := range l.Runs {
			if r.Kind == Prose {
				proseFound = true
			}
		}
	}
	if !proseFound {
		t.Error("expected at least one Prose run alongside the Path span")
	}

	// ── د) سياسة overflow: عند عرض < len(path)، الانقسام عند حدود عنقود ──
	const overflowW = 5
	overflowLines := renderSpans(t, text, []Span{pathSpan}, overflowW,
		Policy{Mode: ReorderAndMirror, Base: Auto})
	for _, l := range overflowLines {
		if l.Width > overflowW {
			t.Errorf("overflow line width %d exceeds limit %d", l.Width, overflowW)
		}
	}
	// الاستعادة من source map تُرجع النص الأصلي عبر RestoreFromSource.
	// لا نستخدم mustRestoreJoin لأنه يضيف \n بين أسطر الالتفاف.
	if got, err := RestoreFromSource(text, clustersOf(overflowLines)); err != nil {
		t.Errorf("overflow RestoreFromSource: %v", err)
	} else if got != text {
		t.Errorf("overflow restore: got %q, want %q", got, text)
	}
}

// ── 5. TestSpanAcrossParagraphBoundary ───────────────────────────────────────
//
// A Span whose byte range straddles the \n paragraph separator must:
//   - succeed (no panic, no ErrInvalidSpans returned by Layout).
//   - produce exactly two visual lines — \n is always a hard paragraph break.
//   - preserve Kind and StyleID on visible runs on both sides of \n.
//   - for Prose kind: keep each paragraph's ParaLevel independent
//     (Arabic paragraph → RTL, Latin paragraph → LTR).
//   - for Code kind: atomicity within each paragraph does not override
//     the hard break at \n.
//
// Input: "مرحبا\nhello" — \n at byte 10, total 16 bytes.
// Span endpoints (0, 16) land on grapheme boundaries.
func TestSpanAcrossParagraphBoundary(t *testing.T) {
	const logical = "\u0645\u0631\u062d\u0628\u0627\nhello"
	if len(logical) != 16 {
		t.Fatalf("test setup: expected len=16, got %d", len(logical))
	}

	tests := []struct {
		name string
		kind SpanKind
	}{
		{
			// Prose: soft-breakable within each paragraph; \n is a hard break.
			// Arabic paragraph → RTL prose runs; Latin paragraph → LTR prose runs.
			name: "prose_across_newline",
			kind: Prose,
		},
		{
			// Code: atomic within each paragraph (LTR island).
			// \n is a hard paragraph boundary and overrides atomicity.
			name: "code_across_newline",
			kind: Code,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			span := Span{Start: 0, End: len(logical), Kind: tc.kind, StyleID: 7}

			// Width 120: wide enough so each paragraph fits on exactly one visual line.
			lines := renderSpans(t, logical, []Span{span}, 120,
				Policy{Mode: ReorderAndMirror, Base: Auto})

			// Contract A: \n is always a hard paragraph break.
			if len(lines) != 2 {
				t.Fatalf("expected 2 visual lines (one per paragraph), got %d", len(lines))
			}

			// Contract B: Kind and StyleID preserved on visible runs on both sides.
			for li, l := range lines {
				if len(l.Runs) == 0 {
					t.Errorf("line[%d]: no runs", li)
					continue
				}
				for _, r := range l.Runs {
					if r.StyleID != 7 {
						t.Errorf("line[%d] Kind=%v: StyleID=%d, want 7", li, r.Kind, r.StyleID)
					}
					if r.Kind != tc.kind {
						t.Errorf("line[%d]: run Kind=%v, want %v", li, r.Kind, tc.kind)
					}
				}
			}

			// Contract C (Prose only): independent ParaLevel per paragraph.
			// Code spans render as LTR islands (even levels) regardless of
			// paragraph direction, so run-level bits are not a useful proxy for
			// ParaLevel there.
			if tc.kind == Prose {
				// First paragraph: Arabic → at least one RTL (odd-level) run.
				arabicRTL := false
				for _, r := range lines[0].Runs {
					if r.Level%2 == 1 {
						arabicRTL = true
					}
				}
				if !arabicRTL {
					t.Errorf("first paragraph (Arabic): expected RTL run; levels=%v",
						collectLevels(lines[0]))
				}

				// Second paragraph: Latin → all even levels (LTR).
				for _, r := range lines[1].Runs {
					if r.Level%2 == 1 {
						t.Errorf("second paragraph (Latin): unexpected RTL run Kind=%v Level=%d",
							r.Kind, r.Level)
					}
				}
			}

			// Contract D: source mapping round-trip — each visual line must
			// restore to the logical text of its paragraph (no \n).
			wantText := []string{"\u0645\u0631\u062d\u0628\u0627", "hello"}
			for i, line := range lines {
				got, err := RestoreFromSource(
					logical,
					clustersOf([]VisualLine{line}),
				)
				if err != nil {
					t.Fatalf("line[%d] RestoreFromSource: %v", i, err)
				}
				if got != wantText[i] {
					t.Errorf("line[%d] restore=%q, want %q", i, got, wantText[i])
				}
			}
		})
	}
}

// ── 6. TestAnalyzeWithLineBreaksInvalidInputs ─────────────────────────────────
//
// AnalyzeWithLineBreaks must return a non-nil error (and must not panic) for
// every invalid combination of text and linebreaks. Two valid cases are also
// included to confirm the guard does not trip on well-formed input.
func TestAnalyzeWithLineBreaksInvalidInputs(t *testing.T) {
	abc := []rune("abc") // len=3; used as reference text

	tests := []struct {
		name       string
		text       []rune
		linebreaks []int
		wantErr    bool
	}{
		// ── invalid ──
		{
			name:       "empty_text",
			text:       []rune{},
			linebreaks: []int{0},
			wantErr:    true,
		},
		{
			name:       "negative_limit",
			text:       abc,
			linebreaks: []int{-1, 3},
			wantErr:    true,
		},
		{
			name:       "duplicate_limit",
			text:       abc,
			linebreaks: []int{2, 2, 3},
			wantErr:    true,
		},
		{
			name:       "descending",
			text:       abc,
			linebreaks: []int{3, 2},
			wantErr:    true,
		},
		{
			name:       "out_of_bounds",
			text:       abc,
			linebreaks: []int{4},
			wantErr:    true,
		},
		{
			name:       "short_terminator",
			text:       abc,
			linebreaks: []int{2},
			wantErr:    true,
		},
		// ── valid ──
		{
			name:       "single_piece",
			text:       abc,
			linebreaks: []int{3},
			wantErr:    false,
		},
		{
			name:       "two_pieces",
			text:       abc,
			linebreaks: []int{1, 3},
			wantErr:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Must not panic regardless of input.
			var gotErr error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("panic: %v", r)
					}
				}()
				_, _, gotErr = bidi.AnalyzeWithLineBreaks(tc.text, -1, tc.linebreaks)
			}()

			if tc.wantErr && gotErr == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.wantErr && gotErr != nil {
				t.Errorf("unexpected error: %v", gotErr)
			}
		})
	}
}
