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
