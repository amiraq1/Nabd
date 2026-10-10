package mcp

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Intentional duplication from internal/tools/bash.go.
//
// These primitives are copied rather than imported so internal/mcp keeps no
// dependency on core packages (ADR-0003 §3). TestMCPPrimitivesMatchBash pins
// the behavioral contract; any intentional change must update both copies.
// Source lines refer to internal/tools/bash.go at the time of copying.
// ---------------------------------------------------------------------------

// mcpChildEnvAllowlist mirrors childEnvAllowlist (bash.go:209).
var mcpChildEnvAllowlist = map[string]bool{
	"PATH":        true, // command resolution; sanitized below (no empty/relative entries)
	"TERM":        true, // terminal capabilities for interactive-ish commands
	"LANG":        true, // locale
	"LC_ALL":      true,
	"LC_COLLATE":  true,
	"LC_CTYPE":    true,
	"LC_MESSAGES": true,
	"LC_MONETARY": true,
	"LC_NUMERIC":  true,
	"LC_TIME":     true,
}

// mcpSanitizePath mirrors sanitizePath (bash.go:270): strips empty and
// relative entries from a PATH value. An empty entry means the current
// working directory on Unix, which would let an attacker-planted binary in
// the project dir shadow real commands.
func mcpSanitizePath(p string) string {
	parts := strings.Split(p, ":")
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		if !strings.HasPrefix(part, "/") {
			continue // relative entry: drop
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, ":")
}

// mcpChildEnv mirrors childEnv (bash.go:229): builds the child environment
// from the explicit allowlist only. Names are emitted in sorted order so
// repeated spawns see a byte-identical environment. NUL bytes are never
// forwarded.
func mcpChildEnv(parent []string) []string {
	src := make(map[string]string, len(parent))
	for _, kv := range parent {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		if strings.IndexByte(v, 0) >= 0 {
			continue // NUL byte: reject, never forward
		}
		src[k] = v
	}

	names := make([]string, 0, len(mcpChildEnvAllowlist))
	for name := range mcpChildEnvAllowlist {
		if _, ok := src[name]; ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	out := make([]string, 0, len(names)+1)
	for _, name := range names {
		v := src[name]
		if name == "PATH" {
			v = mcpSanitizePath(v)
			if v == "" {
				continue // PATH entirely stripped: omit rather than inherit cwd risk
			}
		}
		out = append(out, name+"="+v)
	}
	out = append(out, "NABD=1")
	return out
}

// mcpTempHome mirrors newTempHome/newPrivateTempDir (bash.go:283): an
// application-owned 0700 directory for the child's HOME, never reused.
func mcpTempHome() (string, error) {
	dir, err := os.MkdirTemp("", "nabd-mcp-home-*")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// ---------------------------------------------------------------------------
// Server lifecycle.
// ---------------------------------------------------------------------------

const (
	// mcpTermGrace mirrors bashTermGrace (bash.go:28): SIGTERM, then this
	// grace, then SIGKILL.
	mcpTermGrace = 400 * time.Millisecond

	// stderrRingSize caps retained server stderr: the last 64 KiB, for
	// failure diagnostics only (never shown in the UI unredacted).
	stderrRingSize = 64 * 1024
)

// parseEnvRef extracts VAR from a "${VAR}" reference. Config validation
// already enforces the shape; this is defense in depth at spawn time.
func parseEnvRef(ref string) (string, bool) {
	if !strings.HasPrefix(ref, "${") || !strings.HasSuffix(ref, "}") || len(ref) < 4 {
		return "", false
	}
	inner := ref[2 : len(ref)-1]
	if inner == "" {
		return "", false
	}
	for i, c := range inner {
		ok := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && c >= '0' && c <= '9')
		if !ok {
			return "", false
		}
	}
	return inner, true
}

// stderrRing retains the last stderrRingSize bytes written.
type stderrRing struct {
	mu sync.Mutex
	b  []byte
}

func (r *stderrRing) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.b = append(r.b, p...)
	if len(r.b) > stderrRingSize {
		r.b = r.b[len(r.b)-stderrRingSize:]
	}
	return len(p), nil
}

func (r *stderrRing) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.b)
}

// Server manages one MCP server process: spawn, environment, and guaranteed
// termination. It owns a Client over the server's stdio.
type Server struct {
	id       string
	cmd      *exec.Cmd
	client   *Client
	stdin    io.WriteCloser
	stdoutR  *os.File
	pgid     int
	home     string
	execPath string
	stderr   *stderrRing
	waitCh   chan error
	waitErr  error

	closeOnce sync.Once
}

// Spawn starts an MCP server process for id with a minimal, explicit
// environment: the allowlisted base (PATH sanitized, TERM, LANG, LC_*),
// an isolated 0700 HOME (never the real one), and only the ${VAR}
// references declared in cfg.Env, resolved from our own environment.
// The process runs in its own process group (Setpgid) so the whole group —
// including grandchildren the server spawns — can be signalled at once.
func Spawn(id string, cfg ServerConfig, notify func(Notification)) (*Server, error) {
	if err := platformCheck(); err != nil {
		return nil, err
	}
	if cfg.Transport != "" && cfg.Transport != "stdio" {
		return nil, fmt.Errorf("mcp: server %q: unsupported transport %q", id, cfg.Transport)
	}
	if !cfg.Enabled {
		return nil, fmt.Errorf("mcp: server %q is disabled", id)
	}

	execPath, err := exec.LookPath(cfg.Command)
	if err != nil {
		return nil, fmt.Errorf("mcp: server %q: resolve command: %w", id, err)
	}
	if abs, err := filepath.Abs(execPath); err == nil {
		execPath = abs
	}
	if fi, err := os.Stat(execPath); err != nil {
		return nil, fmt.Errorf("mcp: server %q: stat %s: %w", id, execPath, err)
	} else if fi.IsDir() {
		return nil, fmt.Errorf("mcp: server %q: %s is a directory", id, execPath)
	}

	env := mcpChildEnv(os.Environ())
	home, herr := mcpTempHome()
	if herr == nil {
		env = append(env, "HOME="+home)
	}
	// else: no HOME at all — the caller's real HOME is never passed through.

	names := make([]string, 0, len(cfg.Env))
	for name := range cfg.Env {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		inner, ok := parseEnvRef(cfg.Env[name])
		if !ok {
			continue
		}
		if v := os.Getenv(inner); strings.IndexByte(v, 0) < 0 {
			env = append(env, name+"="+v)
		}
	}

	cmd := exec.Command(execPath, cfg.Args...)
	cmd.Env = env
	cmd.SysProcAttr = mcpSysProcAttr()

	stdinW, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: server %q: stdin pipe: %w", id, err)
	}
	// Real pipes, not StdoutPipe: Wait must not hang on a grandchild holding
	// the write end — mirrors bash.go.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		stdinW.Close()
		return nil, fmt.Errorf("mcp: server %q: stdout pipe: %w", id, err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		stdinW.Close()
		stdoutR.Close()
		stdoutW.Close()
		return nil, fmt.Errorf("mcp: server %q: stderr pipe: %w", id, err)
	}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	if err := cmd.Start(); err != nil {
		stdinW.Close()
		stdoutR.Close()
		stdoutW.Close()
		stderrR.Close()
		stderrW.Close()
		return nil, fmt.Errorf("mcp: server %q: start: %w", id, err)
	}
	stdoutW.Close() // the parent's copies, or readers never see EOF
	stderrW.Close()

	s := &Server{
		id:       id,
		cmd:      cmd,
		stdin:    stdinW,
		stdoutR:  stdoutR,
		pgid:     cmd.Process.Pid,
		home:     home,
		execPath: execPath,
		stderr:   &stderrRing{},
		waitCh:   make(chan error, 1),
	}
	go func() { s.waitCh <- cmd.Wait() }()
	go func() { _, _ = io.Copy(s.stderr, stderrR); stderrR.Close() }()

	s.client = NewClient(stdoutR, stdinW, notify)
	go s.watch()
	return s, nil
}

// watch kills the server group when the client's read loop dies abnormally
// (oversize message, malformed JSON, EOF): fail-closed, no orphan.
func (s *Server) watch() {
	<-s.client.Done()
	if s.client.Err() != nil {
		s.Kill()
	}
}

// Initialize runs the MCP handshake over the spawned server.
func (s *Server) Initialize(ctx context.Context) (string, error) {
	return s.client.Initialize(ctx)
}

// Client returns the JSON-RPC client over this server's stdio.
func (s *Server) Client() *Client { return s.client }

// PGID is the server's process group id (== its pid: Setpgid).
func (s *Server) PGID() int { return s.pgid }

// ExecPath is the resolved absolute executable path (fingerprint input).
func (s *Server) ExecPath() string { return s.execPath }

// StderrTail returns the last stderrRingSize bytes of server stderr,
// for failure diagnostics only.
func (s *Server) StderrTail() string { return s.stderr.String() }

// Close shuts the server down: polite stdin EOF, then the whole process
// group is signalled BEFORE Wait reaps the leader (bash.go "no sweep after
// Wait" rule), so no orphan can survive. Idempotent.
func (s *Server) Close() error {
	s.shutdown(true)
	return nil
}

// Kill terminates the server group immediately. Idempotent.
func (s *Server) Kill() {
	s.shutdown(false)
}

func (s *Server) shutdown(graceful bool) {
	s.closeOnce.Do(func() {
		if graceful {
			_ = s.stdin.Close()
		}
		// Sweep the group before Wait: the pgid is unambiguously ours until
		// reaped, so this cannot signal a reused id.
		mcpKillGroup(s.pgid)
		s.waitErr = <-s.waitCh
		if s.home != "" {
			os.RemoveAll(s.home)
		}
		// Unblock the read loop even if the pipe somehow stayed open.
		s.stdoutR.Close()
		s.client.Close()
	})
}

// ---------------------------------------------------------------------------
// Client observability for the lifecycle monitor.
// ---------------------------------------------------------------------------

// Done is closed when the client's read loop ends.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err returns the read loop's terminal error, or nil on clean shutdown.
func (c *Client) Err() error {
	c.errmu.Lock()
	defer c.errmu.Unlock()
	return c.rerr
}
