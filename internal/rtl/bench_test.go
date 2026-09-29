package rtl

import "testing"

const benchMixed = "\u0645\u0631\u062D\u0628\u0627 \u0628\u0627\u0644\u0639\u0627\u0644\u0645 hello (test) internal/ui/feed.go \u062B\u0645 \u0634\u063A\u0651\u0644 go test ./... \u0648\u0627\u0644\u0646\u062A\u064A\u062C\u0629 123 def"

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
