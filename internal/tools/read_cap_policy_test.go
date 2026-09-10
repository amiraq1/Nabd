package tools

import (
	"path/filepath"
	"testing"

	"nabd/internal/config"
)

// NBD-404: the read cap follows the provider's declaration, and an explicit
// NABD_MAX_READ still outranks it.
//
// The cap is a package-level value resolved once at startup, so these tests
// save and restore it — they mutate the same state production does rather than
// a copy, which is the only way to test the decision that actually ships.

// withReadCap isolates a test from the package-level cap.
func withReadCap(t *testing.T) {
	t.Helper()
	oldCap, oldExplicit := maxReadBytes, maxReadExplicit
	t.Cleanup(func() { maxReadBytes, maxReadExplicit = oldCap, oldExplicit })
}

// TestSetReadCapAppliesTheDeclaredValue is the plumbing half: what the provider
// declares is what the tool enforces.
func TestSetReadCapAppliesTheDeclaredValue(t *testing.T) {
	withReadCap(t)
	maxReadBytes, maxReadExplicit = defaultMaxRead(), false

	for _, want := range []int{8192, 16384} {
		SetReadCap(want)
		if got := maxReadBytes; got != want {
			t.Fatalf("SetReadCap(%d) left the cap at %d", want, got)
		}
	}
}

// TestSetReadCapRespectsAnExplicitOverride is the contract that makes the
// provider policy safe to ship: an operator who set NABD_MAX_READ keeps it,
// because a custom base URL pointed at a metered clone is exactly the case the
// override exists for.
func TestSetReadCapRespectsAnExplicitOverride(t *testing.T) {
	withReadCap(t)
	maxReadBytes, maxReadExplicit = 4096, true

	SetReadCap(16384)
	if got := maxReadBytes; got != 4096 {
		t.Fatalf("a declared provider cap overrode an explicit NABD_MAX_READ: cap is %d, want 4096", got)
	}
}

// TestSetReadCapRejectsUnusableValues proves the bounds are enforced on this
// path too, so a provider cannot widen the cap to something absurd or zero it
// out: a zero cap would produce empty reads the model answers with false
// confidence.
func TestSetReadCapRejectsUnusableValues(t *testing.T) {
	withReadCap(t)
	for _, bad := range []int{0, -1, minMaxRead - 1, maxMaxRead + 1} {
		maxReadBytes, maxReadExplicit = defaultMaxRead(), false
		SetReadCap(bad)
		if got := maxReadBytes; got != defaultMaxRead() {
			t.Errorf("SetReadCap(%d) changed the cap to %d; out-of-range values must be ignored", bad, got)
		}
	}
}

// TestUnusableOverrideDoesNotSuppressTheProvider answers a question the
// precedence raises: an override outside the accepted range is IGNORED, which
// must mean "not set" and not "set to something we refused".
//
// Before this was fixed, NABD_MAX_READ=999999 left the user on the
// conservative fallback while still counting as an explicit choice — so the
// provider's own declaration was suppressed too, and an Anthropic session
// silently ran at Groq's cap. The range guards the user's value; it must not
// also disable the provider policy.
func TestUnusableOverrideDoesNotSuppressTheProvider(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NABD_CONFIG", filepath.Join(dir, "config"))

	for _, bad := range []string{"0", "-5", "999999999", "not-a-number"} {
		if isUsableReadCap(bad) {
			t.Errorf("isUsableReadCap(%q) = true; the value is outside [%d, %d]", bad, minMaxRead, maxMaxRead)
		}

		// The behaviour that matters: with an unusable override, a declared
		// provider cap must still be applied.
		t.Setenv("NABD_MAX_READ", bad)
		config.ResetForTest()
		withReadCap(t)
		maxReadBytes, maxReadExplicit = envMaxRead(), isUsableReadCap(config.Get("NABD_MAX_READ"))
		SetReadCap(16384)
		if got := maxReadBytes; got != 16384 {
			t.Fatalf("NABD_MAX_READ=%q: cap is %d, want the declared provider value 16384 — "+
				"an unusable override must be treated as unset", bad, got)
		}
	}

	// A usable override still wins, which is the whole point of the precedence.
	t.Setenv("NABD_MAX_READ", "4096")
	config.ResetForTest()
	withReadCap(t)
	maxReadBytes, maxReadExplicit = envMaxRead(), isUsableReadCap(config.Get("NABD_MAX_READ"))
	SetReadCap(16384)
	if got := maxReadBytes; got != 4096 {
		t.Fatalf("a usable override was overridden by the provider: cap %d, want 4096", got)
	}
}

// TestEnvMaxReadStillWinsAtStartup checks the resolution order end to end: with
// NABD_MAX_READ set, the startup value is the override and not the fallback.
func TestEnvMaxReadStillWinsAtStartup(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	t.Setenv("NABD_CONFIG", cfg)
	t.Setenv("NABD_MAX_READ", "2048")
	config.ResetForTest()

	if got := envMaxRead(); got != 2048 {
		t.Fatalf("envMaxRead() = %d, want the explicit 2048", got)
	}

	// And with nothing set, the fallback is the conservative default rather
	// than a provider value: no provider has been resolved yet at this point.
	t.Setenv("NABD_MAX_READ", "")
	config.ResetForTest()
	if got := envMaxRead(); got != defaultMaxRead() {
		t.Fatalf("envMaxRead() with nothing set = %d, want the fallback %d", got, defaultMaxRead())
	}
}

// TestDefaultReadCapIsTheConservativeOne pins which value is the fallback. The
// larger cap is applied only when a provider declares it; with no provider
// information the conservative value governs, because silence is not evidence
// of a generous ceiling.
func TestDefaultReadCapIsTheConservativeOne(t *testing.T) {
	if defaultMaxRead() != 3072 {
		t.Fatalf("defaultMaxRead() = %d, want the conservative 3072", defaultMaxRead())
	}
	if got := maxReadBytes; got <= 0 {
		t.Fatalf("package cap is %d; it must be positive at init", got)
	}
}

// TestRegistryReportsReadCap proves the loop can ask the tool layer which cap
// is in force, which is what lets a 413 Notice name it.
func TestRegistryReportsReadCap(t *testing.T) {
	withReadCap(t)
	maxReadBytes, maxReadExplicit = 12345, false

	reg, _ := newReg(t)
	if got := reg.ReadCapBytes(); got != 12345 {
		t.Fatalf("Registry.ReadCapBytes() = %d, want the cap in force (12345)", got)
	}
}
