package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The PR security checklist gate and the threat-model freshness gate must agree
// on which files make docs/THREAT_MODEL.md mandatory. The checklist gate used to
// demand the document from every pull request that checked the item, even though
// the PR template lets that item be satisfied by confirming that no security
// claim changed, so a pull request that changed no security contract could not
// pass the gate at all.
func TestPRChecklistGateScopesThreatModelClaim(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(data)
	}

	checklist := read("scripts/check-pr-security-checklist.sh")
	freshness := read("scripts/check-threat-model-freshness.sh")

	for _, file := range []string{
		"internal/tools/path.go",
		"internal/tools/bash.go",
		"internal/perm/policy.go",
		"internal/config/config.go",
		"internal/snap/shadow.go",
		"internal/safefs",
		"internal/agent/fence.go",
		"cmd/ag/main.go",
	} {
		if !strings.Contains(freshness, file) {
			t.Errorf("check-threat-model-freshness.sh no longer lists %q; keep both gates in sync", file)
		}
		if !strings.Contains(checklist, file) {
			t.Errorf("check-pr-security-checklist.sh missing security-relevant path %q", file)
		}
	}

	if !strings.Contains(checklist, `git diff --name-only "$base" HEAD -- "${security_files[@]}"`) {
		t.Error("checklist gate must scope the docs/THREAT_MODEL.md requirement to security-relevant files")
	}

	// Every required item, and the unconditional regression-test cross-check,
	// must survive the narrowing.
	for _, item := range []string{
		"I classified whether this changes paths",
		"I updated \\`docs/THREAT_MODEL.md\\`",
		"I added or updated a regression test",
		"I checked that logs, fixtures, and diffs contain no credentials",
		"I verified third-party actions are pinned to full commit SHAs",
		"but no *_test.go in diff.",
	} {
		if !strings.Contains(checklist, item) {
			t.Errorf("checklist gate no longer requires %q", item)
		}
	}
}
