package rtl

import (
	"os"
	"testing"
)

const (
	benchArabicPure       = "مرحبا بكم في عالم البرمجة بلغة جو نبض المحرك النصي العربي المتقدم"
	benchArabicMarks      = "بِسْمِ اللَّهِ الرَّحْمَٰنِ الرَّحِيمِ كَتَبَ يُكْتَبُ مَكْتُوبٌ قَرَأَ يَقْرَأُ قِرَاءَةً دَرَسَ يَدْرُسُ دِرَاسَةً"
	benchArabicLamAlef    = "لا إله إلا الله محمد رسول الله الإسلام للأمة وللأولاد والأحفاد والآباء والإخوة"
	benchArabicMixedLatin = "\u0645\u0631\u062D\u0628\u0627 \u0628\u0627\u0644\u0639\u0627\u0644\u0645 hello (test) internal/ui/feed.go \u062B\u0645 \u0634\u063A\u0651\u0644 go test ./... \u0648\u0627\u0644\u0646\u062A\u064A\u062C\u0629 123 def"
)

const benchMixed = benchArabicMixedLatin

func benchmarkText(b *testing.B, text string) {
	b.ReportAllocs()
	shaping := os.Getenv("BENCH_SHAPING") == "1"
	opts := layoutOptions{arabicShaping: shaping}
	for i := 0; i < b.N; i++ {
		if _, err := layoutWithOptions(text, nil, 50, Policy{Mode: ReorderAndMirror, Base: Auto}, opts); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkArabic(b *testing.B) {
	benchmarkText(b, benchArabicPure)
}

func BenchmarkArabicMarks(b *testing.B) {
	benchmarkText(b, benchArabicMarks)
}

func BenchmarkLamAlef(b *testing.B) {
	benchmarkText(b, benchArabicLamAlef)
}

func BenchmarkMixedArabicLatin(b *testing.B) {
	benchmarkText(b, benchArabicMixedLatin)
}

func BenchmarkLayoutMixed(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Layout(benchMixed, nil, 50, Policy{Mode: ReorderAndMirror, Base: Auto}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLayoutASCII(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Layout("internal/ui/feed.go go test ./... and more plain ascii text", nil, 50, Policy{Mode: ReorderAndMirror, Base: LTR}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLayoutLogical(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Layout(benchMixed, nil, 50, Policy{Mode: Logical, Base: Auto}); err != nil {
			b.Fatal(err)
		}
	}
}
