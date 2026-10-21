package perm

import "testing"

// TestClassifyWritePath pins the classification itself: which write targets are
// execution-sensitive, which are repository-internal, and — just as important —
// which ordinary files must keep working untouched.
func TestClassifyWritePath(t *testing.T) {
	cases := []struct {
		path string
		want WritePathTier
	}{
		// Deferred execution: build entry points.
		{"Makefile", WritePathExecutionSensitive},
		{"makefile", WritePathExecutionSensitive},
		{"MAKEFILE", WritePathExecutionSensitive},
		{"build/Makefile", WritePathExecutionSensitive},
		{"GNUmakefile", WritePathExecutionSensitive},
		{"justfile", WritePathExecutionSensitive},
		{"Taskfile.yml", WritePathExecutionSensitive},
		{"Taskfile.yaml", WritePathExecutionSensitive},

		// Deferred execution: package and tool configuration that runs code.
		{"package.json", WritePathExecutionSensitive},
		{"apps/web/package.json", WritePathExecutionSensitive},
		{"PACKAGE.JSON", WritePathExecutionSensitive},
		{".envrc", WritePathExecutionSensitive},
		{"sub/.envrc", WritePathExecutionSensitive},
		{".npmrc", WritePathExecutionSensitive},
		{".pre-commit-config.yaml", WritePathExecutionSensitive},
		{".goreleaser.yaml", WritePathExecutionSensitive},
		{".husky/pre-commit", WritePathExecutionSensitive},
		{".vscode/tasks.json", WritePathExecutionSensitive},
		{".github/workflows/ci.yml", WritePathExecutionSensitive},
		{"nested/.github/workflows/nightly.yaml", WritePathExecutionSensitive},

		// Deferred execution: Git attributes and modules can name a filter or
		// a remote that later runs.
		{".gitattributes", WritePathExecutionSensitive},
		{"vendor/.gitmodules", WritePathExecutionSensitive},

		// Repository-internal state: refused, never asked about.
		{".git/hooks/pre-commit", WritePathInternal},
		{".git/config", WritePathInternal},
		{"sub/.git/hooks/post-checkout", WritePathInternal},
		{".ag/shadow/evil", WritePathInternal},
		{".AG/config", WritePathInternal},

		// Ordinary files must be unaffected.
		{"README.md", WritePathOrdinary},
		{"internal/tools/write.go", WritePathOrdinary},
		{"Makefile.bak", WritePathOrdinary},
		{"makefile2", WritePathOrdinary},
		{"docs/package.json.md", WritePathOrdinary},
		{"notes/.envrc.example", WritePathOrdinary},
		{"src/gitattributes", WritePathOrdinary},
		{"src/git/modules", WritePathOrdinary},
		{"", WritePathOrdinary},
		{".", WritePathOrdinary},
	}

	for _, tc := range cases {
		got, reason := ClassifyWritePath(tc.path)
		if got != tc.want {
			t.Errorf("ClassifyWritePath(%q) = %v (%q), want %v", tc.path, got, reason, tc.want)
		}
		if tc.want != WritePathOrdinary && reason == "" {
			t.Errorf("ClassifyWritePath(%q) must name what it matched", tc.path)
		}
	}
}
