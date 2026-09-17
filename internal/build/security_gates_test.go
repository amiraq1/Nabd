package build

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const validPRBody = "- [x] I classified whether this changes paths\n" +
	"- [x] I updated `docs/THREAT_MODEL.md`\n" +
	"- [x] I added or updated a regression test\n" +
	"- [x] I checked that logs, fixtures, and diffs contain no credentials\n" +
	"- [x] I verified third-party actions are pinned to full commit SHAs\n"

var requiredSecurityPrefixes = []string{
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
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\nOutput: %s", strings.Join(args, " "), err, string(out))
	}
}

func runGitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s failed: %v\nStderr: %s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

func runCmd(t *testing.T, dir string, env []string, name string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	} else {
		cmd.Env = os.Environ()
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("run %s: %v", name, err)
		}
	}
	return exitCode, stdout.String(), stderr.String()
}

func initTestRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.name", "test")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Repo\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "THREAT_MODEL.md"), []byte("# Threat Model\n"), 0o644); err != nil {
		t.Fatalf("write THREAT_MODEL: %v", err)
	}

	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-qm", "initial commit")
	base := runGitOut(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "update-ref", "refs/remotes/origin/master", base)
	return dir, base
}

func gateScripts(t *testing.T) (string, string) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	freshness := filepath.Join(root, "scripts", "check-threat-model-freshness.sh")
	checklist := filepath.Join(root, "scripts", "check-pr-security-checklist.sh")
	if _, err := os.Stat(freshness); err != nil {
		t.Fatalf("freshness script not found: %v", err)
	}
	if _, err := os.Stat(checklist); err != nil {
		t.Fatalf("checklist script not found: %v", err)
	}
	return freshness, checklist
}

func prGateEnv(bodyFile, baseRef string) []string {
	return append(os.Environ(),
		"GITHUB_EVENT_NAME=pull_request",
		"GITHUB_BASE_REF="+baseRef,
		"LIVE_BODY_FILE="+bodyFile,
	)
}

func TestSecurityGates(t *testing.T) {
	freshnessScript, checklistScript := gateScripts(t)

	// Case 1 & Acceptance Criteria: every security boundary prefix (including internal/tools/)
	// without THREAT_MODEL.md update must fail the freshness gate. Deleting any prefix from the
	// gate script causes this test to fail.
	t.Run("Case1_NewFileInSecurityPrefixWithoutThreatModelUpdate_Fails", func(t *testing.T) {
		repoDir, base := initTestRepo(t)
		for _, prefix := range requiredSecurityPrefixes {
			t.Run(prefix, func(t *testing.T) {
				runGit(t, repoDir, "checkout", "-B", "branch-case1", base)
				runGit(t, repoDir, "clean", "-fdx")

				relPath := prefix
				if strings.HasSuffix(prefix, "/") {
					relPath = filepath.Join(prefix, "new_file.go")
				}
				fullPath := filepath.Join(repoDir, relPath)
				if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(fullPath, []byte("package test\n"), 0o644); err != nil {
					t.Fatalf("write file: %v", err)
				}
				runGit(t, repoDir, "add", ".")
				runGit(t, repoDir, "commit", "-qm", "change "+prefix)

				code, _, stderr := runCmd(t, repoDir, nil, "bash", freshnessScript, base)
				if code != 1 {
					t.Fatalf("prefix %q: expected exit 1 from freshness gate, got %d; stderr: %s", prefix, code, stderr)
				}
				if !strings.Contains(stderr, "security-relevant files changed without docs/THREAT_MODEL.md update:") {
					t.Errorf("prefix %q: expected threat model warning in stderr, got: %s", prefix, stderr)
				}
				if !strings.Contains(stderr, relPath) {
					t.Errorf("prefix %q: expected stderr to mention changed file %q, got: %s", prefix, relPath, stderr)
				}
			})
		}
	})

	// Case 2: Documentation-only diff (outside security_files) succeeds without threat model update.
	t.Run("Case2_DocumentationOnlyDiff_Succeeds", func(t *testing.T) {
		repoDir, base := initTestRepo(t)
		runGit(t, repoDir, "checkout", "-B", "branch-case2", base)
		docPath := filepath.Join(repoDir, "docs", "user_guide.md")
		if err := os.WriteFile(docPath, []byte("# Guide\nDocumentation only.\n"), 0o644); err != nil {
			t.Fatalf("write doc: %v", err)
		}
		runGit(t, repoDir, "add", ".")
		runGit(t, repoDir, "commit", "-qm", "docs: add guide")

		code, _, stderr := runCmd(t, repoDir, nil, "bash", freshnessScript, base)
		if code != 0 {
			t.Fatalf("freshness gate failed on doc-only diff with exit %d: %s", code, stderr)
		}

		bodyPath := filepath.Join(repoDir, "live-body.txt")
		if err := os.WriteFile(bodyPath, []byte(validPRBody), 0o600); err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr := runCmd(t, repoDir, prGateEnv(bodyPath, "master"), "bash", checklistScript)
		if code != 0 {
			t.Fatalf("checklist gate failed on doc-only diff with exit %d: %s", code, stderr)
		}
		if !strings.Contains(stdout, "Regression-test cross-check skipped: the diff changes only comments and documentation.") {
			t.Errorf("expected skip notice in stdout, got: %s", stdout)
		}
	})

	// Case 3: Claiming regression test in PR body without *_test.go in diff fails.
	t.Run("Case3_ClaimingRegressionTestWithoutTestFile_Fails", func(t *testing.T) {
		repoDir, base := initTestRepo(t)
		runGit(t, repoDir, "checkout", "-B", "branch-case3", base)
		toolPath := filepath.Join(repoDir, "internal", "tools", "exec.go")
		if err := os.MkdirAll(filepath.Dir(toolPath), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(toolPath, []byte("package tools\nvar Executable = true\n"), 0o644); err != nil {
			t.Fatalf("write tool: %v", err)
		}
		threatPath := filepath.Join(repoDir, "docs", "THREAT_MODEL.md")
		if err := os.WriteFile(threatPath, []byte("# Threat Model\nUpdated entry.\n"), 0o644); err != nil {
			t.Fatalf("write threat model: %v", err)
		}
		runGit(t, repoDir, "add", ".")
		runGit(t, repoDir, "commit", "-qm", "code change without tests")

		bodyPath := filepath.Join(repoDir, "live-body.txt")
		if err := os.WriteFile(bodyPath, []byte(validPRBody), 0o600); err != nil {
			t.Fatal(err)
		}
		code, _, stderr := runCmd(t, repoDir, prGateEnv(bodyPath, "master"), "bash", checklistScript)
		if code != 1 {
			t.Fatalf("expected exit 1 for missing regression test, got %d", code)
		}
		const wantErr = "S7 gate failed: checklist claims 'I added or updated a regression test' but no *_test.go in diff."
		if !strings.Contains(stderr, wantErr) {
			t.Errorf("expected stderr to contain %q, got: %s", wantErr, stderr)
		}
	})

	// Case 4: Base SHA unresolvable fails both gates with explicit expected messages.
	t.Run("Case4_UnresolvableBaseSHA_Fails", func(t *testing.T) {
		repoDir, _ := initTestRepo(t)

		bodyPath := filepath.Join(repoDir, "live-body.txt")
		if err := os.WriteFile(bodyPath, []byte(validPRBody), 0o600); err != nil {
			t.Fatal(err)
		}
		code, _, stderr := runCmd(t, repoDir, prGateEnv(bodyPath, "unresolvable_ref_xyz"), "bash", checklistScript)
		if code != 1 {
			t.Fatalf("expected exit 1 from checklist gate on unresolvable base ref, got %d", code)
		}
		const wantErr = "S7 gate failed: cannot resolve origin/unresolvable_ref_xyz"
		if !strings.Contains(stderr, wantErr) {
			t.Errorf("expected stderr to contain %q, got: %s", wantErr, stderr)
		}

		cleanEnv := make([]string, 0, len(os.Environ()))
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "BASE_SHA=") {
				cleanEnv = append(cleanEnv, e)
			}
		}
		code, _, stderr = runCmd(t, repoDir, cleanEnv, "bash", freshnessScript)
		if code != 2 {
			t.Fatalf("expected exit 2 from freshness gate on missing base SHA, got %d", code)
		}
		if !strings.Contains(stderr, "usage: ") {
			t.Errorf("expected stderr to contain usage message, got: %s", stderr)
		}
	})
}
