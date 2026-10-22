package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prGateEnvWithActorAndAuthor returns the environment slice for the checklist gate test
// helper with GITHUB_ACTOR and PR_AUTHOR_LOGIN set to the given identities.
func prGateEnvWithActorAndAuthor(bodyFile, baseRef, actor, author string) []string {
	return append(os.Environ(),
		"GITHUB_EVENT_NAME=pull_request",
		"GITHUB_BASE_REF="+baseRef,
		"LIVE_BODY_FILE="+bodyFile,
		"GITHUB_ACTOR="+actor,
		"PR_AUTHOR_LOGIN="+author,
	)
}

// TestPRChecklistGateDependabotPath verifies the automated Dependabot path
// introduced in check-pr-security-checklist.sh.
func TestPRChecklistGateDependabotPath(t *testing.T) {
	_, checklistScript := gateScripts(t)

	tests := []struct {
		name       string
		branch     string // optional explicit branch name; defaults to "branch-"+name
		actor      string
		author     string
		files      map[string]string
		body       string // empty means no LIVE_BODY_FILE content needed (automated path exits first)
		wantExit   int
		wantStdout string
		wantStderr string
	}{
		{
			name:   "Positive_DependabotAuthorAndActor_AllowedRootManifests",
			actor:  "dependabot[bot]",
			author: "dependabot[bot]",
			files: map[string]string{
				"go.mod": "module nabd\n\ngo 1.27.0\n",
				"go.sum": "# checksum file\n",
			},
			body:       "",
			wantExit:   0,
			wantStdout: "Dependabot automated path:",
		},
		{
			name:   "Positive_DependabotAuthorAndActor_ThirdPartyBubblesModAndSum",
			actor:  "dependabot[bot]",
			author: "dependabot[bot]",
			files: map[string]string{
				"third_party/bubbles/go.mod": "module nabd/third_party/bubbles\n\ngo 1.27.0\n",
				"third_party/bubbles/go.sum": "# checksum\n",
			},
			body:       "",
			wantExit:   0,
			wantStdout: "Dependabot automated path:",
		},
		{
			name:   "Positive_DependabotAuthorAndActor_WorkflowFiles",
			actor:  "dependabot[bot]",
			author: "dependabot[bot]",
			files: map[string]string{
				".github/workflows/ci.yml":       "name: CI\non: push\n",
				".github/workflows/release.yaml": "name: Release\non: push\n",
			},
			body:       "",
			wantExit:   0,
			wantStdout: "Dependabot automated path:",
		},
		{
			name:   "Negative_DisallowedFileName_GoModEvilSuffix",
			actor:  "dependabot[bot]",
			author: "dependabot[bot]",
			files: map[string]string{
				"go.mod.evil": "module evil\n",
			},
			body:       "",
			wantExit:   1,
			wantStderr: "outside the allowed dependency scope",
		},
		{
			name:   "Negative_DisallowedFileName_GoSumBackupSuffix",
			actor:  "dependabot[bot]",
			author: "dependabot[bot]",
			files: map[string]string{
				"go.sum.backup": "# backup\n",
			},
			body:       "",
			wantExit:   1,
			wantStderr: "outside the allowed dependency scope",
		},
		{
			name:   "Negative_DisallowedFileName_ThirdPartyExecutableCode",
			actor:  "dependabot[bot]",
			author: "dependabot[bot]",
			files: map[string]string{
				"third_party/bubbles/bubbles.go": "package bubbles\n\nfunc Evil() {}\n",
			},
			body:       "",
			wantExit:   1,
			wantStderr: "outside the allowed dependency scope",
		},
		{
			name:   "Negative_DisallowedFileName_ThirdPartyNestedSubtree",
			actor:  "dependabot[bot]",
			author: "dependabot[bot]",
			files: map[string]string{
				"third_party/bubbles/nested/go.mod": "module nested\n",
			},
			body:       "",
			wantExit:   1,
			wantStderr: "outside the allowed dependency scope",
		},
		{
			name:   "Negative_DisallowedFileName_WorkflowNonYamlScript",
			actor:  "dependabot[bot]",
			author: "dependabot[bot]",
			files: map[string]string{
				".github/workflows/deploy.sh": "#!/bin/bash\necho evil\n",
			},
			body:       "",
			wantExit:   1,
			wantStderr: "outside the allowed dependency scope",
		},
		{
			name:   "Negative_DisallowedFileName_WorkflowNestedDir",
			actor:  "dependabot[bot]",
			author: "dependabot[bot]",
			files: map[string]string{
				".github/workflows/sub/ci.yml": "name: CI\n",
			},
			body:       "",
			wantExit:   1,
			wantStderr: "outside the allowed dependency scope",
		},
		{
			name:   "Negative_OutOfScopeSourceFile",
			actor:  "dependabot[bot]",
			author: "dependabot[bot]",
			files: map[string]string{
				"go.mod":              "module nabd\n\ngo 1.27.0\n",
				"internal/tools/x.go": "package tools\n\nvar X = 1\n",
			},
			body:       "",
			wantExit:   1,
			wantStderr: "outside the allowed dependency scope",
		},
		{
			// Verify that an actual branch starting with "dependabot/" does NOT
			// bypass the gate when author and actor are human.
			name:   "Negative_SpoofedBranchPrefix_HumanAuthorAndActor",
			branch: "dependabot/go_modules/malicious-update",
			actor:  "human-user",
			author: "human-user",
			files: map[string]string{
				"go.mod": "module nabd\n\ngo 1.27.0\n",
			},
			body:       "",
			wantExit:   1,
			wantStdout: "",
		},
		{
			// PR authored by Dependabot, but a human collaborator pushed a commit
			name:   "Negative_AuthorActorMismatch_DependabotAuthor_HumanActor",
			branch: "dependabot/go_modules/update",
			actor:  "human-contributor",
			author: "dependabot[bot]",
			files: map[string]string{
				"go.mod": "module nabd\n\ngo 1.27.0\n",
			},
			body:       "",
			wantExit:   1,
			wantStdout: "",
		},
		{
			// Spoofed actor claim: actor set to dependabot[bot], but PR author is human
			name:   "Negative_AuthorActorMismatch_HumanAuthor_DependabotActor",
			branch: "dependabot/go_modules/update",
			actor:  "dependabot[bot]",
			author: "human-user",
			files: map[string]string{
				"go.mod": "module nabd\n\ngo 1.27.0\n",
			},
			body:       "",
			wantExit:   1,
			wantStdout: "",
		},
		{
			name:   "Negative_SpoofedActorName_NotCanonical",
			actor:  "dependabot-impersonator",
			author: "dependabot-impersonator",
			files: map[string]string{
				"go.mod": "module nabd\n\ngo 1.27.0\n",
			},
			body:     "",
			wantExit: 1,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			repoDir, base := initTestRepo(t)
			branchName := tc.branch
			if branchName == "" {
				branchName = "branch-" + tc.name
			}
			runGit(t, repoDir, "checkout", "-B", branchName, base)

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
			bodyContent := tc.body
			if err := os.WriteFile(bodyPath, []byte(bodyContent), 0o600); err != nil {
				t.Fatalf("write live-body: %v", err)
			}

			env := prGateEnvWithActorAndAuthor(bodyPath, "master", tc.actor, tc.author)
			exitCode, stdout, stderr := runCmd(t, repoDir, env, "bash", checklistScript)

			if exitCode != tc.wantExit {
				t.Fatalf("expected exit %d, got %d\nstdout: %s\nstderr: %s",
					tc.wantExit, exitCode, stdout, stderr)
			}
			if tc.wantStdout != "" && !strings.Contains(stdout, tc.wantStdout) {
				t.Errorf("expected stdout to contain %q, got: %s", tc.wantStdout, stdout)
			}
			if tc.wantStderr != "" && !strings.Contains(stderr, tc.wantStderr) {
				t.Errorf("expected stderr to contain %q, got: %s", tc.wantStderr, stderr)
			}
		})
	}
}

// TestPRChecklistGateDependabotAllowedPrefixesDocumented verifies the contract assertions
// in check-pr-security-checklist.sh and .github/workflows/pr-checklist.yml.
func TestPRChecklistGateDependabotAllowedPrefixesDocumented(t *testing.T) {
	checklist := readRepoFile(t, "scripts/check-pr-security-checklist.sh")

	// Both identities must be asserted exactly
	if !strings.Contains(checklist, `"${PR_AUTHOR_LOGIN:-}" == "dependabot[bot]"`) || !strings.Contains(checklist, `"${PR_AUTHOR_LOGIN:-}" == "app/dependabot"`) {
		t.Error("checklist gate must allow only the exact Dependabot PR identities")
	}
	if !strings.Contains(checklist, `"${GITHUB_ACTOR:-}" == "dependabot[bot]"`) {
		t.Error(`checklist gate must check exact actor identity: "${GITHUB_ACTOR:-}" == "dependabot[bot]"`)
	}

	// Must define is_dependabot_allowed_file function
	if !strings.Contains(checklist, "is_dependabot_allowed_file()") {
		t.Error("checklist gate must define is_dependabot_allowed_file() helper")
	}

	// The workflow must inject both GITHUB_ACTOR and PR_AUTHOR_LOGIN
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "pr-checklist.yml"))
	if err != nil {
		t.Fatalf("read pr-checklist.yml: %v", err)
	}
	wfStr := string(workflow)
	if !strings.Contains(wfStr, "GITHUB_ACTOR:") {
		t.Error("pr-checklist.yml must pass GITHUB_ACTOR to the checklist script step")
	}
	if !strings.Contains(wfStr, "PR_AUTHOR_LOGIN:") {
		t.Error("pr-checklist.yml must pass PR_AUTHOR_LOGIN to the checklist script step")
	}
}
