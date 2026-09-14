package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type gitStatusMsg struct {
	branch string
	dirty  int
	at     time.Time
	err    error
}

const gitHeaderInterval = 1 * time.Second

// isGitRepo reports whether dir (or any of its parent directories) contains a
// .git directory or file (supporting both standard repos and worktrees/submodules).
func isGitRepo(dir string) bool {
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	curr := abs
	for {
		gitPath := filepath.Join(curr, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			return true
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}
	return false
}

// gitStatusCmd shells out off the render path. View must never call git.
func gitStatusCmd(dir ...string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", "status", "--porcelain=v2", "--branch")
		if len(dir) > 0 && dir[0] != "" {
			cmd.Dir = dir[0]
		}
		out, err := cmd.Output()
		if err != nil {
			return gitStatusMsg{at: time.Now(), err: err} // silent: the header is a hint
		}
		return parseGitStatus(string(out), time.Now())
	}
}

// parseGitStatus parses porcelain v2 output into a gitStatusMsg.
// It is pure and operates on text alone for deterministic table-driven testing.
func parseGitStatus(out string, now time.Time) gitStatusMsg {
	st := gitStatusMsg{at: now}
	if out == "" {
		return st
	}
	lines := strings.Split(out, "\n")
	for _, l := range lines {
		l = strings.TrimRight(l, "\r\n")
		if strings.HasPrefix(l, "# branch.head ") {
			raw := strings.TrimPrefix(l, "# branch.head ")
			raw = strings.TrimSpace(raw)
			st.branch = SanitizeForDisplay(raw, DisplayPolicy{AllowNewline: false, AllowTab: false, Redact: false})
			continue
		}
		// Porcelain v2 entries:
		// 1: ordinary changed entries
		// 2: renamed or copied entries
		// u: unmerged entries
		// ?: untracked entries
		if strings.HasPrefix(l, "1 ") || strings.HasPrefix(l, "2 ") ||
			strings.HasPrefix(l, "u ") || strings.HasPrefix(l, "? ") {
			st.dirty++
		}
	}
	return st
}

// gitHeaderCandidates builds the candidate ladder for displaying git branch status.
func gitHeaderCandidates(branch string, dirty int) []string {
	if branch == "" {
		return nil
	}
	state := "(clean)"
	if dirty > 0 {
		state = fmt.Sprintf("(%d modified)", dirty)
	}
	return []string{
		fmt.Sprintf("%s %s", branch, state),
		branch,
	}
}

// handleGitStatus updates the feed's git state and manages periodic rescheduling.
func (m *Feed) handleGitStatus(msg gitStatusMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.gitFailures++
		if m.gitFailures >= 2 {
			// Stop rescheduling permanently after two consecutive failures.
			return m, nil
		}
		return m, tea.Tick(gitHeaderInterval, func(t time.Time) tea.Msg {
			return gitStatusCmd(m.gitDir)()
		})
	}
	m.gitFailures = 0
	m.gitBranch = msg.branch
	m.gitDirty = msg.dirty
	m.gitStatusAt = msg.at
	return m, tea.Tick(gitHeaderInterval, func(t time.Time) tea.Msg {
		return gitStatusCmd(m.gitDir)()
	})
}
