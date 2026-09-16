package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// securityPathsFrom extracts the entries inside security_files=( ... ) from a
// gate script. It is used to verify the gate does not reference a path that does
// not exist in the repository (a typo silently weakens the boundary).
func securityPathsFrom(script string) []string {
	start := strings.Index(script, "security_files=(")
	if start < 0 {
		return nil
	}
	rest := script[start+len("security_files=("):]
	end := strings.Index(rest, ")")
	if end < 0 {
		return nil
	}
	body := rest[:end]
	var out []string
	for _, tok := range strings.Fields(body) {
		tok = strings.Trim(tok, `"`)
		if tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

func scriptName(content string) string {
	if strings.Contains(content, "check-pr-security-checklist.sh") {
		return "check-pr-security-checklist.sh"
	}
	return "check-threat-model-freshness.sh"
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

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

	for _, path := range []string{
		"internal/agent/fence.go",
		"internal/config/",
		"internal/perm/",
		"internal/pathindex/",
		"internal/provider/",
		"internal/redact/",
		"internal/safefs/",
		"internal/snap/",
		"internal/store/",
		"internal/tools/",
		"cmd/ag/main.go",
		".goreleaser.yaml",
		"scripts/",
	} {
		if !strings.Contains(freshness, path) {
			t.Errorf("check-threat-model-freshness.sh no longer lists %q; keep both gates in sync", path)
		}
		if !strings.Contains(checklist, path) {
			t.Errorf("check-pr-security-checklist.sh missing security-relevant path %q", path)
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

// TestPRChecklistGateSecurityPathsAreRealBoundaries is the sufficiency assertion
// the parity test could previously only promise. Each directory prefix in the
// gate must be a real directory in the repository, and each single-file entry
// must exist, so a typo in the gate silently weakens the boundary.
func TestPRChecklistGateSecurityPathsAreRealBoundaries(t *testing.T) {
	checklist := readRepoFile(t, "scripts/check-pr-security-checklist.sh")
	freshness := readRepoFile(t, "scripts/check-threat-model-freshness.sh")

	// Parse the security_files=() array from each script. The two scripts keep
	// the same list by construction (the parity test above pins that).
	for _, script := range []string{checklist, freshness} {
		for _, path := range securityPathsFrom(script) {
			if strings.HasSuffix(path, "/") {
				dir := filepath.Join("..", "..", strings.TrimSuffix(path, "/"))
				if !isDir(dir) {
					t.Errorf("%q references directory %q that does not exist in the repo", scriptName(script), path)
				}
				continue
			}
			file := filepath.Join("..", "..", path)
			if !fileExists(file) {
				t.Errorf("%q references file %q that does not exist in the repo", scriptName(script), path)
			}
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
