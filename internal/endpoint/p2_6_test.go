package endpoint_test

import (
	"os"
	"path/filepath"
	"testing"

	"nabd/internal/config"
	"nabd/internal/endpoint"
)

// TestCurrentAllowListIgnoresEnvUnderV2 pins P2-6: under config v2 the implicit
// environment fallback for NABD_ENDPOINT_ALLOW must be disabled, so a stray
// NABD_ENDPOINT_ALLOW in the process environment cannot widen the endpoint
// allowlist beyond what the v2 document declares.
//
// Before this fix CurrentAllowList() fell back to
// os.Getenv("NABD_ENDPOINT_ALLOW") even when config v2 was active,
// re-introducing the exact bypass that v2's fail-closed design was meant to
// prevent.
func TestCurrentAllowListIgnoresEnvUnderV2(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	p := filepath.Join(dir, "config.v2.json")
	body := `{"version":2,"provider":"anthropic","credentials":{"anthropic":{"source":"env"}}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.V2EnvVar, p)
	// A stray environment allowlist must be ignored under v2.
	t.Setenv("NABD_ENDPOINT_ALLOW", "http://127.0.0.1:11434")

	config.ResetForTest()
	t.Cleanup(config.ResetForTest)
	if err := config.Load(); err != nil {
		t.Fatalf("config.Load under v2: %v", err)
	}

	al := endpoint.CurrentAllowList()
	if al != nil {
		t.Fatalf("under v2, CurrentAllowList must ignore NABD_ENDPOINT_ALLOW env; got %+v", al)
	}

	// Sanity: the same value is still honoured under v1, proving the change is
	// scoped to the v2 bypass and did not delete the environment path outright.
	config.ResetForTest()
	t.Setenv(config.V2EnvVar, "")
	t.Setenv(config.EnvVar, "")
	t.Setenv("NABD_ENDPOINT_ALLOW", "http://127.0.0.1:11434")
	if err := config.Load(); err != nil {
		t.Fatalf("config.Load under v1: %v", err)
	}
	if got := endpoint.CurrentAllowList(); got == nil {
		t.Fatal("under v1, CurrentAllowList should honour NABD_ENDPOINT_ALLOW; got nil")
	}
}
