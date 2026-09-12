package build

import "testing"

func TestZeroSafeDefaults(t *testing.T) {
	if Version() != "dev" || Commit() != "none" || Date() != "unknown" {
		t.Fatalf("defaults: version=%q commit=%q date=%q",
			Version(), Commit(), Date())
	}
}

func TestLine(t *testing.T) {
	want := "dev · none · unknown"
	if got := Line(); got != want {
		t.Fatalf("Line() = %q, want %q", got, want)
	}
}

func TestBannerPrefix(t *testing.T) {
	want := "nabd dev · none · unknown"
	if got := BannerPrefix(); got != want {
		t.Fatalf("BannerPrefix() = %q, want %q", got, want)
	}
}
