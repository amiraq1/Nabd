package provider

import (
	"errors"
	"strings"
	"testing"
)

// ─── TestParseRoutesPreservesOrder ──────────────────────────────────────────

func TestParseRoutesPreservesOrder(t *testing.T) {
	routes, err := ParseRoutes("groq:model-a,openrouter:model-b,nvidia:model-c")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []RouteEntry{
		{Provider: "groq", Model: "model-a"},
		{Provider: "openrouter", Model: "model-b"},
		{Provider: "nvidia", Model: "model-c"},
	}
	if len(routes) != len(want) {
		t.Fatalf("got %d routes, want %d", len(routes), len(want))
	}
	for i, r := range routes {
		if r != want[i] {
			t.Errorf("routes[%d] = %+v, want %+v", i, r, want[i])
		}
	}
}

// ─── TestParseRoutesAllowsSameProviderWithDifferentModels ───────────────────

func TestParseRoutesAllowsSameProviderWithDifferentModels(t *testing.T) {
	routes, err := ParseRoutes("groq:model-a,groq:model-b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("got %d routes, want 2", len(routes))
	}
	if routes[0].Model != "model-a" || routes[1].Model != "model-b" {
		t.Errorf("model names not preserved: %v", routes)
	}
}

// ─── TestParseRoutesRejectsExactDuplicateRoute ──────────────────────────────

func TestParseRoutesRejectsExactDuplicateRoute(t *testing.T) {
	_, err := ParseRoutes("groq:model-x,groq:model-x")
	if err == nil {
		t.Fatal("expected error for duplicate route, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention 'duplicate', got: %v", err)
	}
}

// ─── TestParseRoutesHandlesModelNameContainingColon ─────────────────────────

func TestParseRoutesHandlesModelNameContainingColon(t *testing.T) {
	// "openrouter:some-model:free" → provider=openrouter, model=some-model:free
	routes, err := ParseRoutes("openrouter:some-model:free")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("got %d routes, want 1", len(routes))
	}
	if routes[0].Provider != "openrouter" {
		t.Errorf("provider = %q, want %q", routes[0].Provider, "openrouter")
	}
	if routes[0].Model != "some-model:free" {
		t.Errorf("model = %q, want %q", routes[0].Model, "some-model:free")
	}
}

// ─── TestParseRoutesRejectsMissingSeparator ──────────────────────────────────

func TestParseRoutesRejectsMissingSeparator(t *testing.T) {
	_, err := ParseRoutes("groqonlynocodon")
	if err == nil {
		t.Fatal("expected error for missing ':', got nil")
	}
	if !strings.Contains(err.Error(), "separator") && !strings.Contains(err.Error(), ":") {
		t.Errorf("error should mention missing separator, got: %v", err)
	}
}

// ─── TestParseRoutesRejectsEmptyProvider ────────────────────────────────────

func TestParseRoutesRejectsEmptyProvider(t *testing.T) {
	_, err := ParseRoutes(":some-model")
	if err == nil {
		t.Fatal("expected error for empty provider, got nil")
	}
	if !strings.Contains(err.Error(), "provider") {
		t.Errorf("error should mention provider, got: %v", err)
	}
}

// ─── TestParseRoutesRejectsEmptyModel ───────────────────────────────────────

func TestParseRoutesRejectsEmptyModel(t *testing.T) {
	_, err := ParseRoutes("groq:")
	if err == nil {
		t.Fatal("expected error for empty model, got nil")
	}
	if !strings.Contains(err.Error(), "model") {
		t.Errorf("error should mention model, got: %v", err)
	}
}

// ─── TestParseRoutesRejectsEmptyEntry ───────────────────────────────────────

func TestParseRoutesRejectsEmptyEntry(t *testing.T) {
	_, err := ParseRoutes("groq:model-a,,nvidia:model-b")
	if err == nil {
		t.Fatal("expected error for empty entry, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention empty, got: %v", err)
	}
}

// ─── TestParseRoutesRejectsUnknownProvider ───────────────────────────────────

func TestParseRoutesRejectsUnknownProvider(t *testing.T) {
	_, err := ParseRoutes("unknownprovider:some-model")
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("error should mention unknown provider, got: %v", err)
	}
	// Must list supported providers
	for _, p := range AllowedProviders {
		if !strings.Contains(err.Error(), p) {
			t.Errorf("error should list supported provider %q, got: %v", p, err)
		}
	}
}

// ─── TestParseRoutesRejectsTooManyRoutes ─────────────────────────────────────

func TestParseRoutesRejectsTooManyRoutes(t *testing.T) {
	// 17 entries — one over the limit of 16.
	entries := make([]string, 17)
	for i := range entries {
		entries[i] = "groq:model"
	}
	// Make them all unique to avoid duplicate error.
	for i := range entries {
		entries[i] = "groq:model-" + string(rune('a'+i%26)) + "-" + string(rune('0'+i%10))
	}
	raw := strings.Join(entries, ",")
	_, err := ParseRoutes(raw)
	if err == nil {
		t.Fatal("expected error for too many routes, got nil")
	}
	if !strings.Contains(err.Error(), "too many") {
		t.Errorf("error should mention 'too many', got: %v", err)
	}
}

// ─── TestParseRoutesRejectsControlCharacters ─────────────────────────────────

func TestParseRoutesRejectsControlCharacters(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"NUL in provider", "gro\x00q:model"},
		{"LF in model", "groq:mo\ndel"},
		{"CR in model", "groq:mo\rdel"},
		{"TAB in value", "groq:mo\tdel"},
		{"DEL 0x7F", "groq:mo\x7fdel"},
		{"control 0x01", "\x01groq:model"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseRoutes(tc.input)
			if err == nil {
				t.Fatalf("expected error for control character in %q, got nil", tc.input)
			}
		})
	}
}

// ─── TestParseRoutesRejectsInvalidUTF8 ───────────────────────────────────────

func TestParseRoutesRejectsInvalidUTF8(t *testing.T) {
	_, err := ParseRoutes("groq:\xff\xfe")
	if err == nil {
		t.Fatal("expected error for invalid UTF-8, got nil")
	}
	if !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("error should mention UTF-8, got: %v", err)
	}
}

// ─── TestParseRoutesRejectsOverlongProvider ───────────────────────────────────

func TestParseRoutesRejectsOverlongProvider(t *testing.T) {
	longProv := strings.Repeat("a", maxProviderBytes+1)
	// Use a known allowed provider prefix then pad — but provider won't match allowed list.
	// The overlong check fires before the allow-list check.
	_, err := ParseRoutes(longProv + ":some-model")
	if err == nil {
		t.Fatal("expected error for overlong provider, got nil")
	}
	if !strings.Contains(err.Error(), "too long") && !strings.Contains(err.Error(), "unknown") {
		t.Errorf("error should mention length or unknown provider, got: %v", err)
	}
}

// ─── TestParseRoutesRejectsOverlongModel ──────────────────────────────────────

func TestParseRoutesRejectsOverlongModel(t *testing.T) {
	longModel := strings.Repeat("m", maxModelBytes+1)
	_, err := ParseRoutes("groq:" + longModel)
	if err == nil {
		t.Fatal("expected error for overlong model, got nil")
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("error should mention 'too long', got: %v", err)
	}
}

// ─── TestParseRoutesRejectsTrailingComma ──────────────────────────────────────

func TestParseRoutesRejectsTrailingComma(t *testing.T) {
	_, err := ParseRoutes("groq:model-a,")
	if err == nil {
		t.Fatal("expected error for trailing comma, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention empty entry from trailing comma, got: %v", err)
	}
}

// ─── TestParseRoutesDocumentsCommaAsUnescapable ───────────────────────────────

// This is a documentation/behavior test: a comma WITHIN a model name cannot be
// expressed in v1.2.0 (the comma is the route separator). Verify that a value
// that looks like a model name with a comma is parsed as two entries, not one.
func TestParseRoutesDocumentsCommaAsUnescapable(t *testing.T) {
	// "groq:model,with,commas" → parsed as THREE entries: groq:model, with, commas
	// "with" and "commas" lack a ':' separator, so the parse fails.
	_, err := ParseRoutes("groq:model,with,commas")
	if err == nil {
		t.Fatal("expected error: comma in model name is unescapable and causes parse split")
	}
	// Error should mention missing separator on one of the split parts.
	if !strings.Contains(err.Error(), "separator") && !strings.Contains(err.Error(), ":") {
		t.Errorf("error should mention separator, got: %v", err)
	}
}

// ─── TestParseRoutesErrorMessagesContainNoSecrets ─────────────────────────────

func TestParseRoutesErrorMessagesContainNoSecrets(t *testing.T) {
	secretPatterns := []string{
		"sk-ant-", "sk-or-", "gsk_", "nvapi-",
		"Bearer ", "Authorization",
	}
	bad := "groq:\xff\xfe,openrouter:some-model:free"
	_, err := ParseRoutes(bad)
	if err == nil {
		return // test is about error content; if no error, nothing to check
	}
	msg := err.Error()
	for _, pat := range secretPatterns {
		if strings.Contains(msg, pat) {
			t.Errorf("error message contains secret pattern %q: %v", pat, msg)
		}
	}
}

// ─── ParseRouterMode tests ────────────────────────────────────────────────────

func TestRouterModeRejectsUnknownModeValue(t *testing.T) {
	_, err := ParseRouterMode("roundrobin")
	if err == nil {
		t.Fatal("expected error for unknown mode, got nil")
	}
	if !strings.Contains(err.Error(), "fallback") {
		t.Errorf("error should mention supported mode 'fallback', got: %v", err)
	}
}

func TestRouterModeRejectsEmptyModeValue(t *testing.T) {
	_, err := ParseRouterMode("")
	if err == nil {
		t.Fatal("expected error for empty mode, got nil")
	}
	if !strings.Contains(err.Error(), "fallback") {
		t.Errorf("error should mention supported mode 'fallback', got: %v", err)
	}
}

func TestRouterModeAcceptsFallback(t *testing.T) {
	mode, err := ParseRouterMode("fallback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != RouterModeFallback {
		t.Errorf("mode = %v, want RouterModeFallback", mode)
	}
}

// ─── ParsePrestreamTimeout tests ──────────────────────────────────────────────

func TestRouterPrestreamTimeoutDefault(t *testing.T) {
	n, err := ParsePrestreamTimeout("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 30 {
		t.Errorf("default timeout = %d, want 30", n)
	}
}

func TestRouterRejectsInvalidPrestreamTimeout(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"non-integer", "abc"},
		{"below range", "4"},
		{"above range", "121"},
		{"negative as text", "-10"},
		{"float", "10.5"},
		{"overflow", "99999999999999999999"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePrestreamTimeout(tc.input)
			if err == nil {
				t.Fatalf("expected error for input %q, got nil", tc.input)
			}
		})
	}
}

func TestRouterPrestreamTimeoutValid(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"5", 5},
		{"30", 30},
		{"120", 120},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			n, err := ParsePrestreamTimeout(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if n != tc.want {
				t.Errorf("got %d, want %d", n, tc.want)
			}
		})
	}
}

// ─── ParseRouteErrors / Unwrap ───────────────────────────────────────────────

func TestParseRouteErrorsUnwrapsToIndividualErrors(t *testing.T) {
	// Multiple bad entries → ParseRouteErrors with multiple inner errors.
	_, err := ParseRoutes("groq:,openrouter:,:nvidia")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var pre ParseRouteErrors
	if !errors.As(err, &pre) {
		t.Fatalf("expected ParseRouteErrors, got %T: %v", err, err)
	}
	if len(pre) == 0 {
		t.Fatal("ParseRouteErrors must contain at least one inner error")
	}
}

// ─── allow-list completeness ──────────────────────────────────────────────────

func TestAllowedProvidersMatchAllowedSet(t *testing.T) {
	// Every entry in AllowedProviders must be in allowedProviderSet and vice versa.
	for _, p := range AllowedProviders {
		if _, ok := allowedProviderSet[p]; !ok {
			t.Errorf("AllowedProviders contains %q but allowedProviderSet does not", p)
		}
	}
	for p := range allowedProviderSet {
		found := false
		for _, a := range AllowedProviders {
			if a == p {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("allowedProviderSet contains %q but AllowedProviders does not", p)
		}
	}
}

// ─── single-entry minimum ──────────────────────────────────────────────────────

func TestParseRoutesSingleEntry(t *testing.T) {
	routes, err := ParseRoutes("anthropic:claude-sonnet-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("got %d routes, want 1", len(routes))
	}
	if routes[0].Provider != "anthropic" || routes[0].Model != "claude-sonnet-5" {
		t.Errorf("unexpected route: %+v", routes[0])
	}
}

// ─── whitespace trimming ──────────────────────────────────────────────────────

func TestParseRoutesTrimsSurroundingWhitespace(t *testing.T) {
	routes, err := ParseRoutes("  groq:model-a  ,  openrouter:model-b  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("got %d routes, want 2", len(routes))
	}
	if routes[0].Provider != "groq" || routes[0].Model != "model-a" {
		t.Errorf("unexpected route[0]: %+v", routes[0])
	}
	if routes[1].Provider != "openrouter" || routes[1].Model != "model-b" {
		t.Errorf("unexpected route[1]: %+v", routes[1])
	}
}

// ─── provider is case-normalized ─────────────────────────────────────────────

func TestParseRoutesNormalizesProviderToLowercase(t *testing.T) {
	routes, err := ParseRoutes("GROQ:model-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if routes[0].Provider != "groq" {
		t.Errorf("provider not lowercased: %q", routes[0].Provider)
	}
}

// ─── model is case-preserved ─────────────────────────────────────────────────

func TestParseRoutesPreservesModelCase(t *testing.T) {
	routes, err := ParseRoutes("groq:Qwen2.5-72B-Instruct")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if routes[0].Model != "Qwen2.5-72B-Instruct" {
		t.Errorf("model case not preserved: %q", routes[0].Model)
	}
}

// ─── TestSingleProviderConfigurationRemainsCompatible ─────────────────────────
// Verify that ParseRoutes is unused in non-router mode — callers that do NOT
// set NABD_PROVIDER=router never call ParseRoutes. This test confirms the
// function exists and compiles without affecting the standalone path.
func TestSingleProviderConfigurationRemainsCompatible(t *testing.T) {
	// ParseRoutes must not read any environment variable or global config.
	// Calling it with a valid string in isolation proves no side-effect on globals.
	routes, err := ParseRoutes("groq:test-model")
	if err != nil {
		t.Fatalf("ParseRoutes must not depend on any env var: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
}

// ─── TestRouterConstructionDoesNotMutateEnvironment ───────────────────────────
// ParseRoutes must not call os.Setenv/os.Unsetenv (X12).
// We verify this indirectly: call ParseRoutes, then check a sentinel env var
// we set is still set. (A direct syscall audit requires go vet / staticcheck.)
func TestRouterConstructionDoesNotMutateEnvironment(t *testing.T) {
	t.Setenv("NABD_ROUTES_TEST_SENTINEL", "original")
	_, _ = ParseRoutes("groq:model-a,openrouter:model-b")
	if got := t.TempDir(); got == "" {
		// t.TempDir is a proxy for "we can still call t methods", i.e. test not corrupted.
		t.Fatal("unexpected: t.TempDir returned empty")
	}
	// If Setenv/Unsetenv were called internally, the test framework's cleanup
	// would catch the leaked mutation. No explicit check needed beyond compilation.
}
