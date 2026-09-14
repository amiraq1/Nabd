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
	err    error
}

const gitHeaderInterval = 1 * time.Second

// The header must not describe files outside the granted root.
// A parent repository is out of bounds even though git would answer.
func isGitRepo(dir string) bool {
	if dir == "" {
		return false
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(abs, ".git"))
	return err == nil
}

// gitStatusCmd shells out off the render path. View must never call git.
func gitStatusCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", "status", "--porcelain=v2", "--branch")
		// Explicit, filtered env. A nil Env makes the child inherit the full
		// parent environment; we instead forward only what git needs so the
		// header hint can never leak session credentials into a child process.
		cmd.Env = gitChildEnv(os.Environ())
		if dir != "" {
			cmd.Dir = dir
		}
		out, err := cmd.Output()
		if err != nil {
			return gitStatusMsg{err: err} // silent: the header is a hint
		}
		return parseGitStatus(string(out))
	}
}

// gitChildEnv returns a minimal, filtered environment for the header's git
// subprocess. It forwards only the variables required to locate and run git
// (PATH) and present its output (TERM, locale), dropping everything else so
// parent secrets are never inherited.
func gitChildEnv(parent []string) []string {
	allowed := []string{"PATH", "TERM", "LANG", "LC_ALL"}
	keep := make(map[string]string, len(allowed))
	for _, kv := range parent {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		if strings.IndexByte(v, 0) >= 0 {
			continue // NUL byte: reject, never forward
		}
		keep[k] = v
	}
	out := make([]string, 0, len(allowed))
	for _, name := range allowed {
		if v, ok := keep[name]; ok {
			out = append(out, name+"="+v)
		}
	}
	return out
}

// parseGitStatus parses porcelain v2 output into a gitStatusMsg.
// It is pure and operates on text alone for deterministic table-driven testing.
func parseGitStatus(out string) gitStatusMsg {
	var st gitStatusMsg
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
		dir := m.gitDir
		return m, tea.Tick(gitHeaderInterval, func(time.Time) tea.Msg {
			return gitStatusCmd(dir)()
		})
	}
	m.gitFailures = 0
	m.gitBranch = msg.branch
	m.gitDirty = msg.dirty
	dir := m.gitDir
	return m, tea.Tick(gitHeaderInterval, func(time.Time) tea.Msg {
		return gitStatusCmd(dir)()
	})
}
