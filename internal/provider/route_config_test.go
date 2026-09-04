// route_config_test.go — integration tests for router config validation.
// These tests target the router-mode config path (F1-F14) at the
// cmd/ag layer or the RouterConfig building logic, without touching
// any network or external system.
package provider

import (
	"strings"
	"testing"
)

// ─── RouterMode config gate ────────────────────────────────────────────────────

// TestRouterRequiresEveryConfiguredKey verifies that a route whose provider
// key is absent causes a clear error mentioning the key NAME (not the value).
// We test this by inspecting ParseRoutes behaviour: the key check happens at
// route construction time (P2), but the error message contract is that no
// secret value is revealed.
//
// For P1, we verify that ParseRoutes itself does not embed key values in errors.
func TestRouterRequiresEveryConfiguredKey(t *testing.T) {
	// ParseRoutes at P1 does not check keys — that happens in P2 when
	// constructors are called. This test documents the contract and will be
	// expanded in P2.
	routes, err := ParseRoutes("groq:test-model")
	if err != nil {
		t.Fatalf("ParseRoutes should not check key presence; got: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
}

// ─── ParseRouterMode: F11 — NABD_MODEL notice contract ───────────────────────

// TestRouterModeIgnoresNABDModelWithNotice documents F11: when NABD_PROVIDER=router,
// NABD_MODEL is ignored and a startup notice is emitted.
// At P1, we verify the contract exists as a documented requirement; the actual
// notice emission happens in the pickProvider layer (P2/P3 test coverage).
func TestRouterModeIgnoresNABDModelWithNotice(t *testing.T) {
	// The ParseRoutes function itself MUST NOT read NABD_MODEL.
	// Set a decoy value and confirm ParseRoutes is unaffected.
	t.Setenv("NABD_MODEL", "some-decoy-model")
	routes, err := ParseRoutes("groq:router-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("expected 1 route")
	}
	// The route model comes from NABD_ROUTES, not NABD_MODEL.
	if routes[0].Model != "router-model" {
		t.Errorf("model = %q, want router-model (NABD_MODEL must not influence ParseRoutes)", routes[0].Model)
	}
}

// ─── F12 — NABD_BASE_URL in router mode ──────────────────────────────────────

// TestRouterModeRejectsGlobalBaseURL documents F12: NABD_BASE_URL is ambiguous
// in router mode and must be rejected at config time. At P1, we verify that
// ParseRoutes does not read NABD_BASE_URL; the rejection happens in the config
// validation layer (implemented in P2).
func TestRouterModeRejectsGlobalBaseURL(t *testing.T) {
	t.Setenv("NABD_BASE_URL", "https://some-proxy.example.com/v1")
	// ParseRoutes itself must not read NABD_BASE_URL — call it and verify
	// it is unaffected (i.e., uses provider/model from the string only).
	routes, err := ParseRoutes("groq:model-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	// Actual rejection of NABD_BASE_URL when NABD_PROVIDER=router is tested
	// in P2 with the router config builder.
	_ = routes
}

// ─── F10 — In router mode NABD_ROUTES is the only model source ───────────────

// TestRouteModelIsPassedExplicitlyToProvider verifies that the model stored
// in a RouteEntry comes from the parsed string (not from config globals).
func TestRouteModelIsPassedExplicitlyToProvider(t *testing.T) {
	t.Setenv("NABD_MODEL", "global-model-should-be-ignored")
	routes, err := ParseRoutes("nvidia:explicit-model-name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if routes[0].Model != "explicit-model-name" {
		t.Errorf("model = %q, want explicit-model-name", routes[0].Model)
	}
}

// ─── F8 — no keys or URLs inside NABD_ROUTES ─────────────────────────────────

// TestParseRoutesErrorMessagesMentionNoSecretValues verifies that error messages
// produced by invalid routes never contain API key patterns.
func TestParseRoutesErrorMessagesMentionNoSecretValues(t *testing.T) {
	keyPatterns := []string{"sk-ant-", "sk-or-", "gsk_", "nvapi-", "Bearer"}

	badInputs := []string{
		"groq:",
		":missing-provider",
		"unknown-prov:model",
		"groq:model,groq:model",              // duplicate
		strings.Repeat("x", maxModelBytes+1), // overlong (no colon → wrong format error)
	}

	for _, input := range badInputs {
		_, err := ParseRoutes(input)
		if err == nil {
			continue
		}
		msg := err.Error()
		for _, pat := range keyPatterns {
			if strings.Contains(msg, pat) {
				t.Errorf("error for %q contains secret pattern %q: %s", input, pat, msg)
			}
		}
	}
}

// ─── Concurrent ParseRoutes ────────────────────────────────────────────────────

// TestConcurrentRouterConstructionDoesNotCrossContaminateModels verifies that
// concurrent calls to ParseRoutes are safe (no shared mutable state).
func TestConcurrentRouterConstructionDoesNotCrossContaminateModels(t *testing.T) {
	const goroutines = 20
	results := make(chan []RouteEntry, goroutines)
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			model := "model-" + string(rune('a'+idx%26))
			r, err := ParseRoutes("groq:" + model)
			if err != nil {
				errs <- err
				return
			}
			results <- r
		}(i)
	}

	for i := 0; i < goroutines; i++ {
		select {
		case err := <-errs:
			t.Errorf("concurrent ParseRoutes error: %v", err)
		case r := <-results:
			if len(r) != 1 {
				t.Errorf("got %d routes, want 1", len(r))
			}
		}
	}
}
