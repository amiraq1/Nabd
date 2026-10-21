package ui

import (
	"bytes"
	"context"
	"errors"
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
	// sig is the repository signature (see gitRepoSig) captured when the
	// message was produced. unchanged reports that the signature matched the
	// previous poll, so no git subprocess ran and branch/dirty are stale by
	// construction — the handler must keep its cached values.
	sig       string
	unchanged bool
	err       error
}

// gitHeaderInterval is the cadence of header polls. Repository-side changes
// (commits, staging, branch switches) are detected between polls via
// gitRepoSig, which skips spawning git subprocesses when nothing changed;
// every gitForcePollEvery-th poll still runs git to bound working-tree
// staleness for edits made outside the session.
const gitHeaderInterval = 5 * time.Second

const gitForcePollEvery = 6

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

// gitHardeningArgs precede the subcommand. Command-line -c has the highest
// precedence, so these override anything in repository-local .git/config.
var gitHardeningArgs = []string{
	"-c", "core.fsmonitor=false",
	"--no-optional-locks",
}

var errGitConfigDefinesCommands = errors.New(
	"git header: repository config defines filter commands; status skipped")

// repoConfigDefinesCommands reports whether a repository-controlled scope
// defines a clean/process filter driver, which git status would execute on
// stat-dirty files. System and global scopes belong to the user, not to the
// repository (git-lfs installs filter.lfs.* system-wide), so they are
// trusted. Any other scope, including unknown ones, is not.
func repoConfigDefinesCommands(out []byte) bool {
	if len(out) == 0 {
		return false
	}
	fields := bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0})
	if len(fields)%2 != 0 {
		return true // malformed: fail closed
	}
	for i := 0; i < len(fields); i += 2 {
		switch string(fields[i]) {
		case "system", "global":
			continue
		}
		key, _, _ := bytes.Cut(fields[i+1], []byte{'\n'})
		k := strings.ToLower(string(key))
		if strings.HasPrefix(k, "filter.") &&
			(strings.HasSuffix(k, ".clean") || strings.HasSuffix(k, ".process")) {
			return true
		}
	}
	return false
}

// gitConfigDefinesCommands reads all scopes with --show-scope and delegates
// the trust decision to repoConfigDefinesCommands (system and global scopes
// are trusted; repository-controlled scopes are not). Reading config executes
// nothing.
func gitConfigDefinesCommands(ctx context.Context, dir string, env []string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "config", "--null", "--list", "--includes", "--show-scope")
	cmd.Env = env
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return repoConfigDefinesCommands(out), nil
}

// gitRepoSig summarizes the repository metadata a header poll depends on:
// HEAD (branch switches, commits) and the index (staging). The header skips
// spawning its two git subprocesses while the signature is unchanged, which
// is what makes the 5s poll cadence cheap when the session is idle.
//
// It deliberately does NOT cover working-tree edits: those are caught by the
// periodic forced poll (every gitForcePollEvery-th tick) and by clearing the
// feed's stored signature when a tool that can write files completes.
func gitRepoSig(dir string) string {
	gitDir := filepath.Join(dir, ".git")
	// Resolve worktree/submodule pointer files ("gitdir: <path>").
	if fi, err := os.Stat(gitDir); err == nil && !fi.IsDir() {
		if data, err := os.ReadFile(gitDir); err == nil {
			if target, ok := strings.CutPrefix(string(data), "gitdir: "); ok {
				target = strings.TrimSpace(target)
				if target != "" && !filepath.IsAbs(target) {
					target = filepath.Join(dir, target)
				}
				if target != "" {
					gitDir = filepath.Clean(target)
				}
			}
		}
	}
	var b strings.Builder
	for _, p := range []string{filepath.Join(gitDir, "HEAD"), filepath.Join(gitDir, "index")} {
		fi, err := os.Stat(p)
		if err != nil {
			b.WriteString("x;")
			continue
		}
		fmt.Fprintf(&b, "%d/%d;", fi.ModTime().UnixNano(), fi.Size())
	}
	return b.String()
}

// gitStatusCmd shells out off the render path. View must never call git.
//
// When force is false and lastSig matches the current repository signature,
// no subprocess runs and the message reports unchanged: true. The caller
// passes the feed's stored signature and requests force=true for the first
// poll, for retries after failures, and for the periodic forced poll.
func gitStatusCmd(dir, lastSig string, force bool) tea.Cmd {
	return func() tea.Msg {
		sig := gitRepoSig(dir)
		if !force && lastSig != "" && sig == lastSig {
			return gitStatusMsg{unchanged: true, sig: sig}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		env := gitChildEnv(os.Environ())

		defines, err := gitConfigDefinesCommands(ctx, dir, env)
		if err != nil {
			return gitStatusMsg{err: err, sig: sig} // fail closed, silent: the header is a hint
		}
		if defines {
			return gitStatusMsg{err: errGitConfigDefinesCommands, sig: sig}
		}

		args := append(append([]string{}, gitHardeningArgs...),
			"status", "--porcelain=v2", "--branch", "--ignore-submodules=all")
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Env = env
		if dir != "" {
			cmd.Dir = dir
		}
		out, err := cmd.Output()
		if err != nil {
			return gitStatusMsg{err: err, sig: sig}
		}
		msg := parseGitStatus(string(out))
		msg.sig = sig
		return msg
	}
}

// gitChildEnv returns a minimal, filtered environment for the header's git
// subprocess. It forwards only the variables required to locate and run git
// (PATH) and present its output (TERM, locale), dropping everything else so
// parent secrets (API keys, session tokens, git config overrides) are never
// inherited by the child.
//
// HOME is intentionally omitted: the child git then runs with no user config,
// so it can neither read ~/.gitconfig nor apply a global safe.directory. On a
// single-user Termux environment this is harmless. Note that only the global
// (per-user) config is dropped; /etc/gitconfig (system) still applies, so a
// dubious-ownership rejection surfaces only when the repo owner differs and no
// system-level safe.directory exception exists.
//
// Output order follows the parent environment, not the allowlist, because the
// switch appends each match in the order it appears in parent.
//
// The returned slice is always non-nil: exec.Cmd treats Env == nil as "inherit
// the full parent environment", so an empty result must be a non-nil slice,
// never nil.
func gitChildEnv(parent []string) []string {
	const (
		path  = "PATH"
		term  = "TERM"
		lang  = "LANG"
		lcAll = "LC_ALL"
	)
	out := make([]string, 0, 4)
	for _, kv := range parent {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		// Defense in depth: os.Environ() cannot return NUL bytes (its entries
		// are themselves NUL-terminated), but reject any just in case the input
		// ever changes shape upstream.
		if strings.IndexByte(v, 0) >= 0 {
			continue
		}
		switch k {
		case path, term, lang, lcAll:
			out = append(out, k+"="+v)
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

// scheduleGitPoll arms the next header poll. force=true runs the real git
// subprocesses even when the repository signature is unchanged (first poll,
// retry after failure, periodic forced poll).
func (m *Feed) scheduleGitPoll(force bool) tea.Cmd {
	m.gitPolls++
	nextForce := force || m.gitPolls%gitForcePollEvery == 0
	dir, lastSig := m.gitDir, m.gitRepoSig
	return tea.Tick(gitHeaderInterval, func(time.Time) tea.Msg {
		return gitStatusCmd(dir, lastSig, nextForce)()
	})
}

// handleGitStatus updates the feed's git state and manages periodic rescheduling.
func (m *Feed) handleGitStatus(msg gitStatusMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.gitFailures++
		if m.gitFailures >= 2 {
			// Stop rescheduling permanently after two consecutive failures.
			return m, nil
		}
		// Retry the real poll (not the signature shortcut): the failure may
		// be transient, and the stored signature is left alone so a later
		// success re-syncs it.
		return m, m.scheduleGitPoll(true)
	}
	m.gitFailures = 0
	if !msg.unchanged {
		m.gitBranch = msg.branch
		m.gitDirty = msg.dirty
		m.gitRepoSig = msg.sig
	}
	return m, m.scheduleGitPoll(false)
}
