package build

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReleasePipelineContracts(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(data)
	}

	goreleaser := read(".goreleaser.yaml")
	for _, required := range []string{
		"artifacts: binary",
		"{{ .ArtifactName }}.sbom.json",
		"artifacts: checksum",
	} {
		if !strings.Contains(goreleaser, required) {
			t.Errorf(".goreleaser.yaml missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"artifacts: archive",
		"${artifact}.sbom.json",
		"artifacts: sbom",
	} {
		if strings.Contains(goreleaser, forbidden) {
			t.Errorf(".goreleaser.yaml contains obsolete or duplicate contract %q", forbidden)
		}
	}
	if !strings.Contains(goreleaser, "--output-signature=") {
		t.Errorf(".goreleaser.yaml no longer signs through cosign's legacy --output-signature/--output-certificate flags")
	}

	release := read(".github/workflows/release.yml")
	if !strings.Contains(release, `cosign-release: "v2.`) {
		t.Errorf("release.yml must pin cosign to a v2 major: cosign v3 ignores --output-signature/--output-certificate in favour of --bundle, so the checksum manifest would go unsigned and the release would abort at the signing step")
	}

	ci := read(".github/workflows/ci.yml")
	for _, required := range []string{
		"Threat model test citations",
		"bash scripts/check-threat-model-tests.sh",
		"release-dryrun:",
		"release --clean --snapshot --skip=publish,sign,announce",
		"expected 1 SBOM",
	} {
		if !strings.Contains(ci, required) {
			t.Errorf("ci.yml missing %q", required)
		}
	}

	releasing := read("docs/RELEASING.md")
	for _, required := range []string{
		"$TMPDIR/nabd-linux-amd64",
		"cosign verify-blob",
		"--certificate-identity-regexp",
		"sha256sum --check --ignore-missing checksums.txt",
		"Do not move or retag `v1.5.0`",
	} {
		if !strings.Contains(releasing, required) {
			t.Errorf("RELEASING.md missing %q", required)
		}
	}
}

func TestGitattributesContracts(t *testing.T) {
	root := filepath.Join("..", "..")
	data, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
	if err != nil {
		t.Fatalf("read .gitattributes: %v", err)
	}
	content := string(data)
	for _, required := range []string{
		"*.go text eol=lf",
		"*.sh text eol=lf",
	} {
		if !strings.Contains(content, required) {
			t.Errorf(".gitattributes missing %q", required)
		}
	}
}

// TestReleaseNotesExtractedFromChangelog asserts the release job passes
// GoReleaser a notes file extracted from the matching CHANGELOG section, and that
// the extractor fails closed when that section is missing or empty.
func TestReleaseNotesExtractedFromChangelog(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	release := readRepoFile(t, ".github/workflows/release.yml")
	if !strings.Contains(release, "scripts/release-notes.sh") {
		t.Error("release.yml must extract release notes with scripts/release-notes.sh")
	}
	if !strings.Contains(release, "--release-notes=") {
		t.Error("release.yml must pass the extracted notes to GoReleaser via --release-notes")
	}

	script := filepath.Join(root, "scripts", "release-notes.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("scripts/release-notes.sh not found: %v", err)
	}

	// The v2.1.0 section is present and non-empty: its body must come out, and
	// neither its header nor the next section may leak in.
	code, stdout, stderr := runCmd(t, root, nil, "bash", script, "v2.1.0")
	if code != 0 {
		t.Fatalf("release-notes.sh v2.1.0 exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "Streamed-chunk redaction") {
		t.Errorf("notes do not contain the v2.1.0 body: %q", stdout)
	}
	if strings.Contains(stdout, "## v2.1.0") || strings.Contains(stdout, "## v2.0.0") {
		t.Errorf("notes must be the section body without headers or the next section: %q", stdout)
	}

	// A missing section fails closed.
	code, _, stderr = runCmd(t, root, nil, "bash", script, "v9.9.9")
	if code == 0 {
		t.Error("a missing CHANGELOG section must fail closed")
	}
	if !strings.Contains(stderr, "no CHANGELOG section") {
		t.Errorf("missing-section error is unclear: %s", stderr)
	}

	// An empty section fails closed, driven through the CHANGELOG_FILE override.
	dir := t.TempDir()
	empty := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(empty, []byte("# Changelog\n\n## v9.9.9\n\n## v9.9.8\n\n- older\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "CHANGELOG_FILE="+empty)
	code, _, stderr = runCmd(t, dir, env, "bash", script, "v9.9.9")
	if code == 0 {
		t.Error("an empty CHANGELOG section must fail closed")
	}
	if !strings.Contains(stderr, "no CHANGELOG section") {
		t.Errorf("empty-section error is unclear: %s", stderr)
	}
}

func TestReleaseNotesWrittenOutsideWorkspace(t *testing.T) {
	release := readRepoFile(t, ".github/workflows/release.yml")

	if strings.Contains(release, "> release-notes.md") {
		t.Error("release.yml must not write release notes directly to the workspace (relative path > release-notes.md)")
	}
	if strings.Contains(release, "--release-notes=release-notes.md") {
		t.Error("release.yml must not pass relative --release-notes=release-notes.md")
	}

	if !strings.Contains(release, `"${RUNNER_TEMP}/release-notes.md"`) {
		t.Error(`release.yml extraction step must write to "${RUNNER_TEMP}/release-notes.md"`)
	}
	if !strings.Contains(release, "--release-notes=${{ runner.temp }}/release-notes.md") {
		t.Error("release.yml GoReleaser step must use --release-notes=${{ runner.temp }}/release-notes.md")
	}
}

func TestReleaseAssertsCleanTreeBeforeGoReleaser(t *testing.T) {
	checkCleanStep := func(name, content, extractToken, goreleaserToken string) {
		t.Helper()
		idxExtract := strings.Index(content, extractToken)
		if idxExtract == -1 {
			t.Fatalf("%s missing extraction token %q", name, extractToken)
		}
		idxGoreleaser := strings.Index(content, goreleaserToken)
		if idxGoreleaser == -1 {
			t.Fatalf("%s missing goreleaser token %q", name, goreleaserToken)
		}

		cleanPattern := `test -z "$(git status --porcelain)"`
		idxClean := strings.Index(content, cleanPattern)
		if idxClean == -1 {
			t.Fatalf("%s missing clean tree assertion %q", name, cleanPattern)
		}

		if !(idxExtract < idxClean && idxClean < idxGoreleaser) {
			t.Errorf("%s step order must be extract (%d) < clean tree (%d) < goreleaser (%d)",
				name, idxExtract, idxClean, idxGoreleaser)
		}
	}

	release := readRepoFile(t, ".github/workflows/release.yml")
	checkCleanStep("release.yml", release, "scripts/release-notes.sh", "goreleaser/goreleaser-action")

	ci := readRepoFile(t, ".github/workflows/ci.yml")
	start := strings.Index(ci, "release-dryrun:")
	if start == -1 {
		t.Fatal("ci.yml missing release-dryrun job")
	}
	end := strings.Index(ci[start:], "\n  termux:")
	if end == -1 {
		end = len(ci) - start
	}
	dryrunBlock := ci[start : start+end]
	checkCleanStep("ci.yml (release-dryrun)", dryrunBlock, "scripts/release-notes.sh", "goreleaser/goreleaser-action")
}

// TestReleaseConfigIsValidatedPerPullRequest asserts the dry-run job validates
// .goreleaser.yaml with the same pinned GoReleaser build the release uses, and
// does so before it exercises the release command: a config that no longer
// loads then fails on the pull request instead of at tag time. The keyless
// signing path stays tag-only (it needs OIDC and writes Rekor entries), so this
// per-PR parse is the only automated check of the config itself.
func TestReleaseConfigIsValidatedPerPullRequest(t *testing.T) {
	ci := readRepoFile(t, ".github/workflows/ci.yml")
	start := strings.Index(ci, "release-dryrun:")
	if start == -1 {
		t.Fatal("ci.yml missing release-dryrun job")
	}
	end := strings.Index(ci[start:], "\n  termux:")
	if end == -1 {
		end = len(ci) - start
	}
	dryrun := ci[start : start+end]

	idxCheck := strings.Index(dryrun, "args: check")
	if idxCheck == -1 {
		t.Fatal("ci.yml release-dryrun must validate the GoReleaser config with `args: check`")
	}
	idxRelease := strings.Index(dryrun, "args: release --clean --snapshot")
	if idxRelease == -1 {
		t.Fatal("ci.yml release-dryrun missing the snapshot release step")
	}
	if idxCheck > idxRelease {
		t.Error("the config check must run before the snapshot release, not after it")
	}

	// Both GoReleaser steps must be pinned to the same action commit as the
	// release, and each is a step of its own: validating the config with a
	// different build would validate a config the release never runs.
	goreleaserRE := regexp.MustCompile(`(?m)^\s*uses: goreleaser/goreleaser-action@([0-9a-f]{40}) # (\S+)$`)
	relUses := goreleaserRE.FindAllStringSubmatch(readRepoFile(t, ".github/workflows/release.yml"), -1)
	if len(relUses) != 1 {
		t.Fatalf("release.yml must use goreleaser/goreleaser-action exactly once, got %d", len(relUses))
	}
	ciUses := goreleaserRE.FindAllStringSubmatch(dryrun, -1)
	if len(ciUses) != 2 {
		t.Fatalf("ci.yml release-dryrun must use goreleaser/goreleaser-action twice (config check and snapshot release), got %d", len(ciUses))
	}
	for i, m := range ciUses {
		if m[1] != relUses[0][1] || m[2] != relUses[0][2] {
			t.Errorf("ci.yml goreleaser-action use #%d pins @%s (%s) but release.yml pins @%s (%s); the two must be the same build",
				i+1, m[1], m[2], relUses[0][1], relUses[0][2])
		}
	}

	rel := readRepoFile(t, ".github/workflows/release.yml")
	versionRE := regexp.MustCompile(`(?m)^\s*version: "(v[0-9.]+)"$`)
	ciVersions := versionRE.FindAllStringSubmatch(dryrun, -1)
	relVersions := versionRE.FindAllStringSubmatch(rel, -1)
	if len(ciVersions) == 0 || len(relVersions) == 0 {
		t.Fatalf("could not find the GoReleaser version pin: ci=%v release=%v", ciVersions, relVersions)
	}
	for _, v := range ciVersions {
		if v[1] != relVersions[0][1] {
			t.Errorf("ci.yml validates with GoReleaser %s but the release runs %s", v[1], relVersions[0][1])
		}
	}
}

// TestWorkflowActionsArePinnedToCommitSHAs enforces in code the rule the PR
// checklist states in prose: every third-party action is pinned to a full
// commit SHA, so a moved tag cannot change what CI runs. The walk covers every
// workflow file, so a new workflow inherits the rule.
func TestWorkflowActionsArePinnedToCommitSHAs(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatalf("glob workflows: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no workflow files found; the walker is broken, not the tree")
	}

	usesRE := regexp.MustCompile(`^\s*(?:- )?uses:\s*(\S+)`)
	pinnedRE := regexp.MustCompile(`^[^@]+@[0-9a-f]{40}$`)
	total := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			m := usesRE.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			ref := strings.TrimSuffix(m[1], "\r")
			if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "docker://") {
				continue // a local or docker reference has no commit to pin
			}
			total++
			if !pinnedRE.MatchString(ref) {
				t.Errorf("%s:%d: %q is not pinned to a full commit SHA", filepath.Base(f), i+1, ref)
			}
		}
	}
	if total == 0 {
		t.Fatal("no action references found; the walker is broken, not the tree")
	}
}

func TestReleaseDryrunExtractsNotes(t *testing.T) {
	ci := readRepoFile(t, ".github/workflows/ci.yml")
	start := strings.Index(ci, "release-dryrun:")
	if start == -1 {
		t.Fatal("ci.yml missing release-dryrun job")
	}
	end := strings.Index(ci[start:], "\n  termux:")
	if end == -1 {
		end = len(ci) - start
	}
	dryrunBlock := ci[start : start+end]

	if !strings.Contains(dryrunBlock, "scripts/release-notes.sh") {
		t.Error("ci.yml release-dryrun must execute scripts/release-notes.sh")
	}
	if !strings.Contains(dryrunBlock, "--release-notes=${{ runner.temp }}/release-notes.md") {
		t.Error("ci.yml release-dryrun must pass --release-notes=${{ runner.temp }}/release-notes.md to goreleaser")
	}
	if !strings.Contains(dryrunBlock, "CHANGELOG.md") {
		t.Error("ci.yml release-dryrun must extract version from CHANGELOG.md")
	}
	if strings.Contains(dryrunBlock, "GITHUB_REF_NAME") {
		t.Error("ci.yml release-dryrun must not rely on GITHUB_REF_NAME")
	}
}
