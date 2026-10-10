package build

import (
	"os/exec"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// MCP dependency isolation guard (ADR-0003 §12)
//
// WHY: ADR-0003 mandates that MCP SDK and protocol details stay confined to
// the integration layer (internal/mcp) and never leak into the core packages.
// This guard runs `go list -deps` on each core package and fails if any
// dependency falls outside the pinned allowlist (current core deps + stdlib).
// It covers transitive dependencies by nature of -deps.
//
// The guard passes today and must fail on any future leak (MCP SDK, net/http,
// or any new external module) into the core. See ADR-0003 §12 and §16/B5.
//
// SCOPE: nabd/internal/tools, nabd/internal/perm, nabd/internal/safefs,
// nabd/internal/redact, nabd/internal/toolvocab, nabd/internal/agent.
var mcpIsolationCorePkgs = []string{
	"nabd/internal/tools",
	"nabd/internal/perm",
	"nabd/internal/safefs",
	"nabd/internal/redact",
	"nabd/internal/toolvocab",
	"nabd/internal/agent",
}

// allowedDep reports whether dep is permitted in a core package.
// Allowed: stdlib (no dot in module path, or stdlib-internal like
// crypto/internal/...), and the currently-pinned external deps.
func allowedDep(dep string) bool {
	// nabd's own packages are always fine.
	if strings.HasPrefix(dep, "nabd/") {
		return true
	}
	// Vendored deps (pinned in the repo).
	if strings.HasPrefix(dep, "vendor/") {
		return true
	}
	// stdlib: no dot before the first slash, e.g. "fmt", "net/http",
	// "crypto/tls".
	if idx := strings.Index(dep, "/"); idx >= 0 {
		if !strings.Contains(dep[:idx], ".") {
			return true
		}
	} else if !strings.Contains(dep, ".") {
		return true
	}
	// stdlib-internal: crypto/internal/..., runtime/internal/..., etc.
	// These ship with the toolchain (e.g. crypto/internal/entropy/v1.0.0).
	for _, p := range []string{"crypto/internal/", "runtime/internal/", "internal/"} {
		if strings.HasPrefix(dep, p) {
			return true
		}
	}
	// Pinned external: golang.org/x/sys/unix only.
	if dep == "golang.org/x/sys/unix" {
		return true
	}
	return false
}

func TestMCPDependencyIsolation(t *testing.T) {
	for _, pkg := range mcpIsolationCorePkgs {
		out, err := exec.Command("go", "list", "-deps", pkg).Output()
		if err != nil {
			t.Fatalf("go list -deps %s: %v", pkg, err)
		}
		for _, dep := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			if !allowedDep(dep) {
				t.Errorf("core package %s leaks external dep %q (ADR-0003 §12); "+
					"move it to internal/mcp or update the allowlist with architectural review",
					pkg, dep)
			}
		}
	}
}
