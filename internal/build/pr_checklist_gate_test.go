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

func TestPRChecklistGateNoTestExemption(t *testing.T) {
	_, checklistScript := gateScripts(t)

	type testCase struct {
		name       string
		body       string
		files      map[string]string
		wantExit   int
		wantStdout string
		wantStderr string
	}

	tests := []testCase{
		{
			name: "Positive_DocsOnly_WithNADocumentation",
			body: validPRBody + "\nN/A: documentation\n",
			files: map[string]string{
				"docs/user_guide.md": "# User Guide\nDocumentation content.\n",
			},
			wantExit: 0,
		},
		{
			name: "Positive_GoCommentsOnly_WithNADocs",
			body: validPRBody + "\nN/A: docs\n",
			files: map[string]string{
				"cmd/ag/dummy.go": "// Package main dummy comment\n// Another comment\n",
			},
			wantExit: 0,
		},
		{
			name: "Positive_DependencyOnly_WithNADependency",
			body: validPRBody + "\nN/A: dependency\n",
			files: map[string]string{
				"go.mod": "module test\n",
			},
			wantExit: 0,
		},
		{
			name: "Positive_GateScriptChange_WithTestFile_Succeeds",
			body: validPRBody,
			files: map[string]string{
				"scripts/check-pr-security-checklist.sh": "# modified checklist gate\n",
				"docs/THREAT_MODEL.md":                   "# Threat Model\nUpdated entry.\n",
				"internal/build/new_gate_test.go":        "package build\n",
			},
			wantExit: 0,
		},
		{
			name: "Negative_WorkflowChange_RefusesNAExemption",
			body: validPRBody + "\nN/A: workflow\n",
			files: map[string]string{
				".github/workflows/ci.yml": "# CI workflow\nname: CI\n",
			},
			wantExit:   1,
			wantStderr: "S7 gate failed: checklist claims 'I added or updated a regression test' but no *_test.go in diff.",
		},
		{
			name: "Negative_GateScriptChange_RefusesNAExemption",
			body: validPRBody + "\nN/A: documentation\n",
			files: map[string]string{
				"scripts/check-pr-security-checklist.sh": "# modified checklist gate\n",
				"docs/THREAT_MODEL.md":                   "# Threat Model\nUpdated entry.\n",
			},
			wantExit:   1,
			wantStderr: "S7 gate failed: checklist claims 'I added or updated a regression test' but no *_test.go in diff.",
		},
		{
			name: "Negative_InlineNA_Refused",
			body: validPRBody + "\nSome prefix text N/A: dependency\n",
			files: map[string]string{
				"go.mod": "module test\n",
			},
			wantExit:   1,
			wantStderr: "S7 gate failed: checklist claims 'I added or updated a regression test' but no *_test.go in diff.",
		},
		{
			name: "Negative_NAWithNoKeyword",
			body: validPRBody + "\nN/A:\n",
			files: map[string]string{
				"go.mod": "module test\n",
			},
			wantExit:   1,
			wantStderr: "S7 gate failed: checklist claims 'I added or updated a regression test' but no *_test.go in diff.",
		},
		{
			name: "Negative_NAWithInvalidKeyword",
			body: validPRBody + "\nN/A: none\n",
			files: map[string]string{
				"go.mod": "module test\n",
			},
			wantExit:   1,
			wantStderr: "S7 gate failed: checklist claims 'I added or updated a regression test' but no *_test.go in diff.",
		},
		{
			name: "Negative_MissingNAWithoutTestFile",
			body: validPRBody,
			files: map[string]string{
				"go.mod": "module test\n",
			},
			wantExit:   1,
			wantStderr: "S7 gate failed: checklist claims 'I added or updated a regression test' but no *_test.go in diff.",
		},
		{
			name: "Negative_ExecutableGoDiffWithNADependency",
			body: validPRBody + "\nN/A: dependency\n",
			files: map[string]string{
				"cmd/ag/dummy.go": "package main\n\nvar ExecutableCode = 42\n",
			},
			wantExit:   1,
			wantStderr: "S7 gate failed: checklist claims 'I added or updated a regression test' but no *_test.go in diff.",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			repoDir, base := initTestRepo(t)
			runGit(t, repoDir, "checkout", "-B", "branch-"+tc.name, base)
			for relPath, content := range tc.files {
				fullPath := filepath.Join(repoDir, relPath)
				if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
					t.Fatalf("write file: %v", err)
				}
			}
			runGit(t, repoDir, "add", ".")
			runGit(t, repoDir, "commit", "-qm", "test commit for "+tc.name)

			bodyPath := filepath.Join(repoDir, "live-body.txt")
			if err := os.WriteFile(bodyPath, []byte(tc.body), 0o600); err != nil {
				t.Fatalf("write live-body: %v", err)
			}

			exitCode, stdout, stderr := runCmd(t, repoDir, prGateEnv(bodyPath, "master"), "bash", checklistScript)
			if exitCode != tc.wantExit {
				t.Fatalf("expected exit %d, got %d\nstdout: %s\nstderr: %s", tc.wantExit, exitCode, stdout, stderr)
			}
			if tc.wantStderr != "" && !strings.Contains(stderr, tc.wantStderr) {
				t.Errorf("expected stderr to contain %q, got: %s", tc.wantStderr, stderr)
			}
			if tc.wantStdout != "" && !strings.Contains(stdout, tc.wantStdout) {
				t.Errorf("expected stdout to contain %q, got: %s", tc.wantStdout, stdout)
			}
		})
	}
}

// The checklist previously required every item to be ticked, so an item that did
// not apply had to be ticked falsely or the pull request could not pass. An item
// may now be marked `- [x] N/A: <reason>` on its own line, with a reason of at
// least 10 characters. The escape is bounded: the three items that state an
// invariant which always applies to every pull request (classification,
// credential hygiene, action pinning) can never be N/A, and an unchecked item
// still fails.
func TestPRChecklistGateExplicitNA(t *testing.T) {
	_, checklistScript := gateScripts(t)

	const (
		classifyItem = "- [x] I classified whether this changes paths"
		threatItem   = "- [x] I updated `docs/THREAT_MODEL.md`"
		credItem     = "- [x] I checked that logs, fixtures, and diffs contain no credentials"
	)

	tests := []struct {
		name       string
		body       string
		wantExit   int
		wantStderr string
	}{
		{
			name:     "Positive_ValidNA_OnNonMandatoryItem",
			body:     strings.Replace(validPRBody, threatItem, "- [x] N/A: no security claim changed here", 1),
			wantExit: 0,
		},
		{
			name:       "Negative_ShortNAReason",
			body:       strings.Replace(validPRBody, threatItem, "- [x] N/A: short", 1),
			wantExit:   1,
			wantStderr: "N/A reason is shorter than 10 characters",
		},
		{
			name:       "Negative_NAOnMandatoryItem",
			body:       strings.Replace(validPRBody, classifyItem, "- [x] N/A: it always applies but was marked anyway", 1),
			wantExit:   1,
			wantStderr: "always applies and cannot be marked N/A",
		},
		{
			name:       "Negative_UntickedItem",
			body:       strings.Replace(validPRBody, credItem, "- [ ] "+strings.TrimPrefix(credItem, "- [x] "), 1),
			wantExit:   1,
			wantStderr: "PR security checklist is incomplete",
		},
		{
			name:     "Positive_NormalTickedPR",
			body:     validPRBody,
			wantExit: 0,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			repoDir, base := initTestRepo(t)
			runGit(t, repoDir, "checkout", "-B", "branch-"+tc.name, base)
			docPath := filepath.Join(repoDir, "docs", "user_guide.md")
			if err := os.WriteFile(docPath, []byte("# Guide\nDocumentation only.\n"), 0o644); err != nil {
				t.Fatalf("write doc: %v", err)
			}
			runGit(t, repoDir, "add", ".")
			runGit(t, repoDir, "commit", "-qm", "docs-only for "+tc.name)

			bodyPath := filepath.Join(repoDir, "live-body.txt")
			if err := os.WriteFile(bodyPath, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}

			exitCode, _, stderr := runCmd(t, repoDir, prGateEnv(bodyPath, "master"), "bash", checklistScript)
			if exitCode != tc.wantExit {
				t.Fatalf("expected exit %d, got %d\nstderr: %s", tc.wantExit, exitCode, stderr)
			}
			if tc.wantStderr != "" && !strings.Contains(stderr, tc.wantStderr) {
				t.Errorf("expected stderr to contain %q, got: %s", tc.wantStderr, stderr)
			}
		})
	}
}
