package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// The PR security checklist gate and the threat-model freshness gate must agree
// on which files make docs/THREAT_MODEL.md mandatory. The checklist gate used to
// demand the document from every pull request that checked the item, even though
// the PR template lets that item be satisfied by confirming that no security
// claim changed, so a pull request that changed no security contract could not
// pass the gate at all.
func TestPRChecklistGateScopesThreatModelClaim(t *testing.T) {
	checklist := readRepoFile(t, "scripts/check-pr-security-checklist.sh")
	freshness := readRepoFile(t, "scripts/check-threat-model-freshness.sh")

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

	// Every required item, and the regression-test cross-check itself, must
	// survive the narrowing.
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

// The regression-test cross-check used to be unconditional, which combined with
// the Phase 1 requirement that every item be checked made a comment-only or
// documentation-only pull request impossible to merge. The escape must stay
// narrow: it is granted by inspecting the diff, never by wording in the body,
// and it must not survive a single executable line.
func TestPRChecklistGateExemptsOnlyBehaviourFreeDiffs(t *testing.T) {
	checklist := readRepoFile(t, "scripts/check-pr-security-checklist.sh")

	if !strings.Contains(checklist, "behaviour_free_diff() {") {
		t.Fatal("checklist gate must decide the regression-test escape from the diff, via behaviour_free_diff")
	}

	// The escape is reached only through the helper, and only after the
	// *_test.go check has already failed.
	if !strings.Contains(checklist, "if behaviour_free_diff; then") {
		t.Error("the regression-test escape must be guarded by behaviour_free_diff")
	}

	for _, rule := range []string{
		// Go files are admitted only when every changed line is a comment or blank.
		`[[ -z "$content" || $content == //* ]] || return 1`,
		// Markdown and docs/ carry no behaviour.
		"*.md | docs/*) continue ;;",
		// Everything else -- scripts, workflows, fixtures -- is rejected outright.
		"*) return 1 ;;",
	} {
		if !strings.Contains(checklist, rule) {
			t.Errorf("behaviour_free_diff must keep the rule %q", rule)
		}
	}

	// The failure path must remain: an executable change with no test still fails.
	if !strings.Contains(checklist, "S7 gate failed: checklist claims 'I added or updated a regression test' but no *_test.go in diff.") {
		t.Error("the regression-test requirement must still fail loudly for behaviour-changing diffs")
	}

	// The body must never be able to grant the escape by itself.
	if strings.Contains(checklist, "no test is applicable") && !strings.Contains(checklist, "behaviour_free_diff") {
		t.Error("the escape must not be grantable by body wording")
	}
}
