package build

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSecurityInvariantsRgFailClosed runs the RG_EXEC_COVERAGE_VACUOUS_PASS
// regression test from the repository root and requires it to pass.
//
// The shell test proves two things: (1) check-security-invariants.sh fails
// closed when `rg` is absent from PATH (no vacuous green), and (2) the normal
// path still passes when `rg` is present (or SKIP on hosts without rg).
// Wiring it here makes the pr-checklist S7 gate count this PR's regression
// test (shell-only tests do not satisfy the *_test.go requirement).
func TestSecurityInvariantsRgFailClosed(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	cmd := exec.Command("bash", "scripts/test-security-invariants-rg.sh")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test-security-invariants-rg.sh failed: %v\n%s", err, out)
	}
}
