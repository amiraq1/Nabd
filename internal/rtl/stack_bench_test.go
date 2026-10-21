package rtl

import "testing"

// BenchmarkStackRuns measures the standalone shaping engine across the
// stackRunes decode-buffer sizes (64/128/256). The buffer size is a compile
// time constant, so each size requires a rebuild:
//
//	go test -run="^$" -bench=StackRuns -benchtime=10s -count=1 -cpu=1 ./internal/rtl
func BenchmarkStackRuns(b *testing.B) {
	inputs := map[string]string{
		"ArabicMarks":      benchArabicMarks,
		"MixedArabicLatin": benchArabicMixedLatin,
	}

	for name, s := range inputs {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_ = ShapeArabic(s)
			}
		})
	}
}
