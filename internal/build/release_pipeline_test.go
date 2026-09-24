package build

import (
	"os"
	"path/filepath"
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
