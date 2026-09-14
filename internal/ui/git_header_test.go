package ui

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseGitStatusTable(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		input      string
		wantBranch string
		wantDirty  int
	}{
		{
			name: "clean branch",
			input: strings.Join([]string{
				"# branch.oid 5b0ecee0d56cd45922d8b968034d7e6bfcca7bd4",
				"# branch.head main",
				"# branch.upstream origin/main",
				"# branch.ab +0 -0",
			}, "\n"),
			wantBranch: "main",
			wantDirty:  0,
		},
		{
			name: "dirty branch with modified, renamed, unmerged, untracked",
			input: strings.Join([]string{
				"# branch.oid 1234567890abcdef",
				"# branch.head feature/ui-header",
				"1 .M N... 100644 100644 100644 a b main.go",
				"2 R. N... 100644 100644 100644 a b R100 old.go new.go",
				"u UU N... 100644 100644 100644 100644 conflict.go",
				"? untracked.txt",
			}, "\n"),
			wantBranch: "feature/ui-header",
			wantDirty:  4,
		},
		{
			name: "detached HEAD",
			input: strings.Join([]string{
				"# branch.oid 3319d160d56cd45922d8b968034d7e6bfcca7bd4",
				"# branch.head (detached)",
				"1 .M N... 100644 100644 100644 a b main.go",
			}, "\n"),
			wantBranch: "(detached)",
			wantDirty:  1,
		},
		{
			name:       "empty output",
			input:      "",
			wantBranch: "",
			wantDirty:  0,
		},
		{
			name:       "not a git repository",
			input:      "fatal: not a git repository (or any of the parent directories): .git\n",
			wantBranch: "",
			wantDirty:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGitStatus(tt.input, now)
			if got.branch != tt.wantBranch {
				t.Errorf("branch = %q, want %q", got.branch, tt.wantBranch)
			}
			if got.dirty != tt.wantDirty {
				t.Errorf("dirty = %d, want %d", got.dirty, tt.wantDirty)
			}
			if !got.at.Equal(now) {
				t.Errorf("at = %v, want %v", got.at, now)
			}
		})
	}
}

func TestGitFailureOrTimeoutIsSilent(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	// Git status fails with an error (e.g. timeout or command failure)
	_, _ = f.Update(gitStatusMsg{at: time.Now(), err: errors.New("context deadline exceeded")})

	// 1. Header line is empty
	if h := f.headerText(80); h != "" {
		t.Fatalf("expected empty header on git failure, got %q", h)
	}
	lm := f.computeLayout()
	if lm.HeaderRows != 0 {
		t.Fatalf("expected HeaderRows == 0 on git failure, got %d", lm.HeaderRows)
	}

	// 2. Zero error notices added to feed
	if len(f.notices) != 0 {
		t.Fatalf("git failure leaked error notices into feed: %v", f.notices)
	}
	// 3. Status is untouched
	if f.status != "" {
		t.Fatalf("git failure set status to %q", f.status)
	}
}

func TestGitBranchSanitization(t *testing.T) {
	input := strings.Join([]string{
		"# branch.head \x1b[31;1mfeat\x1b[0m\x00\x07",
		"1 .M file.go",
	}, "\n")
	st := parseGitStatus(input, time.Now())
	if st.branch != "feat" {
		t.Fatalf("expected ANSI escapes and control characters stripped, got %q", st.branch)
	}

	// Bidi overrides (RLO/PDF) must be stripped
	bidiInput := "# branch.head \u202Ereversed\u202Cbranch\n"
	stBidi := parseGitStatus(bidiInput, time.Now())
	if strings.Contains(stBidi.branch, "\u202E") || strings.Contains(stBidi.branch, "\u202C") {
		t.Fatalf("bidi overrides were not stripped from branch name: %q", stBidi.branch)
	}
	if stBidi.branch != "reversedbranch" {
		t.Fatalf("unexpected branch text after bidi stripping: %q", stBidi.branch)
	}

	// Natural Arabic text is preserved
	arabicInput := "# branch.head فرع-تطوير\n"
	stArabic := parseGitStatus(arabicInput, time.Now())
	if stArabic.branch != "فرع-تطوير" {
		t.Fatalf("expected natural Arabic branch preserved, got %q", stArabic.branch)
	}
}

func TestTwoConsecutiveFailuresStopsRescheduling(t *testing.T) {
	f := NewFeed()

	// First failure: gitFailures becomes 1, returns a tick cmd to retry
	_, cmd1 := f.Update(gitStatusMsg{at: time.Now(), err: errors.New("timeout 1")})
	if f.gitFailures != 1 {
		t.Fatalf("expected gitFailures == 1, got %d", f.gitFailures)
	}
	if cmd1 == nil {
		t.Fatal("expected retry tick cmd after first failure")
	}

	// Second consecutive failure: gitFailures becomes 2, returns nil cmd (stops rescheduling)
	_, cmd2 := f.Update(gitStatusMsg{at: time.Now(), err: errors.New("timeout 2")})
	if f.gitFailures != 2 {
		t.Fatalf("expected gitFailures == 2, got %d", f.gitFailures)
	}
	if cmd2 != nil {
		t.Fatalf("expected nil cmd after 2 consecutive failures, got %v", cmd2)
	}

	// A third failure also returns nil cmd
	_, cmd3 := f.Update(gitStatusMsg{at: time.Now(), err: errors.New("timeout 3")})
	if cmd3 != nil {
		t.Fatalf("expected nil cmd for subsequent failures, got %v", cmd3)
	}

	// A successful status resets failure counter
	_, cmdSuccess := f.Update(gitStatusMsg{branch: "main", dirty: 0, at: time.Now()})
	if f.gitFailures != 0 {
		t.Fatalf("expected gitFailures reset to 0, got %d", f.gitFailures)
	}
	if cmdSuccess == nil {
		t.Fatal("expected rescheduling cmd on success")
	}
}

func TestIsGitRepoDetection(t *testing.T) {
	// Current repository must report true
	if !isGitRepo(".") {
		t.Fatal("expected isGitRepo('.') to be true in git workspace")
	}

	// Isolated empty temp dir must report false
	tempDir := t.TempDir()
	if isGitRepo(tempDir) {
		t.Fatalf("expected isGitRepo(%q) to be false for empty directory", tempDir)
	}

	// Feed in empty directory must not start polling in Init() even if gitHeaderEnabled is true
	f := NewFeed()
	f.SetGitHeader(true)
	f.SetGitDir(tempDir)
	if cmd := f.Init(); cmd != nil {
		t.Fatalf("expected Init() to return nil cmd in non-git directory, got %v", cmd)
	}

	// Feed in git directory with gitHeaderEnabled returns polling cmd in Init()
	fGit := NewFeed()
	fGit.SetGitHeader(true)
	fGit.SetGitDir(".")
	if cmd := fGit.Init(); cmd == nil {
		t.Fatal("expected Init() to return polling cmd when git header enabled in git repo")
	}
}

func TestGitHeaderDegradationLadder(t *testing.T) {
	f := NewFeed()
	f.SetGitHeader(true)
	f.gitBranch = "feature/ui-p8-responsive-chrome"
	f.gitDirty = 3

	// At wide width: shows branch and modified count
	wide := f.headerText(80)
	if !strings.Contains(wide, "feature/ui-p8-responsive-chrome (3 modified)") {
		t.Fatalf("wide header want full status, got %q", wide)
	}

	// At medium width that only fits branch name:
	// Branch length is 31 cells. Width 35 fits branch (31) but not branch + " (3 modified)" (44).
	med := f.headerText(35)
	if med != "feature/ui-p8-responsive-chrome" {
		t.Fatalf("medium header want branch only, got %q", med)
	}

	// At narrow width below branch length: drops cleanly to empty string
	narrow := f.headerText(20)
	if narrow != "" {
		t.Fatalf("narrow header want empty string, got %q", narrow)
	}

	// Combined with base header
	f.SetHeader("nabd")
	combined := f.headerText(80)
	if !strings.Contains(combined, "nabd · feature/ui-p8-responsive-chrome (3 modified)") {
		t.Fatalf("combined header want base + git status, got %q", combined)
	}
}
