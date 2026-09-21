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
		"expected 5 SBOMs",
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
