package main

import (
	"os"
	"strings"
	"testing"

	"nabd/internal/provider"
)

// The CLI catalog is the only Arabic home for provider error text (ADR-0002
// decision 4, provider leg). These are the exact user-visible shapes master
// shipped before the move, frozen as literals deliberately: the provider
// package no longer carries a source to extract them from.
func TestProviderLanguageCatalogIsInstalledByteIdentical(t *testing.T) {
	installProviderLanguage()

	if got, want := provider.FirstByteTimeoutMessage(), "الخادم لم يبدأ الردّ خلال 25 ثانية — جرّب NABD_MODEL آخر"; got != want {
		t.Fatalf("first-byte timeout message changed:\n got %q\nwant %q", got, want)
	}

	hint := "provider openai model gpt-x was rejected; run `nabd models openai` to see the live model list"
	cases := []struct {
		name   string
		model  string
		status int
		suffix string
		want   string
	}{
		{
			name:   "anthropic 404/410 shape",
			model:  "claude-x",
			status: 410,
			want:   "الموديل \"claude-x\" غير متاح على هذا الخادم (410)",
		},
		{
			name:   "openai-compatible shape with lookup hint",
			model:  "gpt-x",
			status: 404,
			suffix: ".\n" + hint,
			want:   "الموديل \"gpt-x\" غير متاح على هذا الخادم (404).\n" + hint,
		},
		{
			name:   "openai-compatible shape with empty hint",
			model:  "gpt-x",
			status: 404,
			suffix: ".\n",
			want:   "الموديل \"gpt-x\" غير متاح على هذا الخادم (404).\n",
		},
	}
	for _, tc := range cases {
		if got := provider.ModelUnavailableMessage(tc.model, tc.status, tc.suffix); got != tc.want {
			t.Fatalf("%s changed:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// The install is only effective if main runs it before any entry point can
// stream a provider answer. The call sites are inside main(), so this asserts
// against the source of cmd/ag/main.go (the shape TestFeedIsTheDefaultInteractiveUI
// uses for the same reason).
func TestMainInstallsProviderLanguageBeforeFlagParse(t *testing.T) {
	data, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	src := string(data)

	install := strings.Index(src, "installProviderLanguage()")
	if install < 0 {
		t.Fatal("main.go must call installProviderLanguage()")
	}
	parse := strings.Index(src, "flag.Parse()")
	if parse < 0 {
		t.Fatal("main.go must still parse flags")
	}
	if install > parse {
		t.Fatal("installProviderLanguage() must run before flag.Parse(), so no CLI path can observe the ASCII baseline")
	}
}
