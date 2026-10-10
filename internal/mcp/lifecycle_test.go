package mcp

// Phase 2 ID -> test mapping (ADR-0003 P9):
//   (lifecycle) TestMCPPrimitivesMatchBash, TestMCPLifecycleSpawnHandshake,
//               TestMCPLifecycleEnvIsolation, TestMCPLifecycleStderrRing
//   S1          TestMCPS1NoOrphans
//   S2          TestMCPS2OversizeKillsServer

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func mockServerCfg(t *testing.T, args ...string) ServerConfig {
	t.Helper()
	return ServerConfig{
		Enabled:   true,
		Transport: "stdio",
		Command:   "python3",
		Args:      append([]string{mockServerPath(t)}, args...),
	}
}

func spawnMockServer(t *testing.T, cfg ServerConfig) *Server {
	t.Helper()
	skipOnWindows(t)
	s, err := Spawn("test", cfg, nil)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func lifecycleCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// TestMCPPrimitivesMatchBash pins the behavioral contract of the primitives
// intentionally duplicated from internal/tools/bash.go (ADR-0003 §3).
func TestMCPPrimitivesMatchBash(t *testing.T) {
	t.Run("sanitizePath", func(t *testing.T) {
		cases := []struct{ in, want string }{
			{"/usr/bin:/bin", "/usr/bin:/bin"},
			{"/usr/bin::/bin", "/usr/bin:/bin"},
			{"relative:/usr/bin", "/usr/bin"},
			{"", ""},
			{":", ""},
			{"/a:./b:/c", "/a:/c"},
			{"/single", "/single"},
		}
		for _, tc := range cases {
			if got := mcpSanitizePath(tc.in); got != tc.want {
				t.Errorf("mcpSanitizePath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		}
	})

	t.Run("childEnv", func(t *testing.T) {
		parent := []string{
			"PATH=/usr/bin:relative:/bin",
			"TERM=xterm-256color",
			"HOME=/real/home",
			"SECRET=topsecret",
			"LANG=en_US.UTF-8",
			"MALFORMED",
			"NULBAD=a\x00b",
			"LC_TIME=C",
		}
		got := mcpChildEnv(parent)
		want := []string{
			"LANG=en_US.UTF-8",
			"LC_TIME=C",
			"PATH=/usr/bin:/bin",
			"TERM=xterm-256color",
			"NABD=1",
		}
		if len(got) != len(want) {
			t.Fatalf("mcpChildEnv = %q, want %q", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("mcpChildEnv = %q, want %q", got, want)
			}
		}

		// PATH entirely stripped -> omitted, not inherited.
		got2 := mcpChildEnv([]string{"PATH=relative:./x"})
		if len(got2) != 1 || got2[0] != "NABD=1" {
			t.Fatalf("stripped PATH should be omitted, got %q", got2)
		}
	})

	t.Run("killGroupGuard", func(t *testing.T) {
		// pgid <= 1 must be a no-op, never a hang or a signal to init.
		mcpKillGroup(0)
		mcpKillGroup(1)
		mcpKillGroup(-5)
	})

	t.Run("tempHome", func(t *testing.T) {
		home, err := mcpTempHome()
		if err != nil {
			t.Fatalf("mcpTempHome: %v", err)
		}
		defer os.RemoveAll(home)
		fi, err := os.Stat(home)
		if err != nil {
			t.Fatalf("stat temp home: %v", err)
		}
		if !fi.IsDir() || fi.Mode().Perm() != 0o700 {
			t.Fatalf("temp home mode = %o, want 700", fi.Mode().Perm())
		}
		if !strings.Contains(home, "nabd-mcp-home-") {
			t.Fatalf("unexpected temp home path %q", home)
		}
	})
}

func TestMCPLifecycleSpawnHandshake(t *testing.T) {
	s := spawnMockServer(t, mockServerCfg(t, "--protocol-version", "2025-11-25"))
	ctx, cancel := lifecycleCtx(t)
	defer cancel()

	negotiated, err := s.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if negotiated != "2025-11-25" {
		t.Fatalf("negotiated = %q", negotiated)
	}
	if !strings.HasPrefix(s.ExecPath(), "/") {
		t.Fatalf("ExecPath not absolute: %q", s.ExecPath())
	}
	if s.PGID() <= 1 {
		t.Fatalf("bad pgid %d", s.PGID())
	}

	tools, err := s.Client().ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if mcpGroupAlive(s.PGID()) {
		t.Fatal("process group survived Close")
	}
}

func TestMCPLifecycleDisabledRefused(t *testing.T) {
	skipOnWindows(t)
	cfg := mockServerCfg(t)
	cfg.Enabled = false
	if _, err := Spawn("test", cfg, nil); err == nil {
		t.Fatal("expected refusal for disabled server")
	}
}

// TestMCPLifecycleEnvIsolation: the spawned process sees only the allowlisted
// base environment plus declared ${VAR} refs — never the real HOME and never
// undeclared secrets (MCP-SEC-01 at the process level).
func TestMCPLifecycleEnvIsolation(t *testing.T) {
	t.Setenv("NABD_TEST_SECRET", "super-secret-value")
	t.Setenv("NABD_TEST_PUBLIC", "public-value")

	cfg := mockServerCfg(t)
	cfg.Env = map[string]string{"MCP_PUBLIC": "${NABD_TEST_PUBLIC}"}
	s := spawnMockServer(t, cfg)
	ctx, cancel := lifecycleCtx(t)
	defer cancel()

	if _, err := s.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	res, err := s.Client().CallTool(ctx, "__dump_env", nil)
	if err != nil {
		t.Fatalf("dump_env: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("empty dump_env result")
	}
	var envMap map[string]string
	if err := json.Unmarshal([]byte(res.Content[0].Text), &envMap); err != nil {
		t.Fatalf("parse dumped env: %v", err)
	}

	if _, ok := envMap["NABD_TEST_SECRET"]; ok {
		t.Fatal("undeclared secret reached the server environment")
	}
	if envMap["MCP_PUBLIC"] != "public-value" {
		t.Fatalf("declared ref not resolved: %q", envMap["MCP_PUBLIC"])
	}
	if envMap["NABD"] != "1" {
		t.Fatal("NABD=1 marker missing")
	}
	home := envMap["HOME"]
	if home == "" {
		t.Fatal("server has no HOME")
	}
	if home == os.Getenv("HOME") {
		t.Fatal("server inherited the real HOME")
	}
	if !strings.Contains(home, "nabd-mcp-home-") {
		t.Fatalf("HOME is not the isolated temp dir: %q", home)
	}
	for _, part := range strings.Split(envMap["PATH"], ":") {
		if part == "" || !strings.HasPrefix(part, "/") {
			t.Fatalf("PATH not sanitized: %q", envMap["PATH"])
		}
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("isolated HOME %q not removed on Close", home)
	}
}

// TestMCPS1NoOrphans: after Close, the whole process group — including a
// grandchild the server spawned — is gone. Three rounds.
func TestMCPS1NoOrphans(t *testing.T) {
	for round := 0; round < 3; round++ {
		s := spawnMockServer(t, mockServerCfg(t, "--spawn-sleeper"))
		ctx, cancel := lifecycleCtx(t)

		if _, err := s.Initialize(ctx); err != nil {
			cancel()
			t.Fatalf("round %d: Initialize: %v", round, err)
		}
		pgid := s.PGID()
		if !mcpGroupAlive(pgid) {
			cancel()
			t.Fatalf("round %d: group not alive after spawn", round)
		}
		cancel()
		if err := s.Close(); err != nil {
			t.Fatalf("round %d: Close: %v", round, err)
		}

		deadline := time.Now().Add(10 * time.Second)
		for mcpGroupAlive(pgid) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if mcpGroupAlive(pgid) {
			t.Fatalf("round %d: process group %d survived Close (orphan)", round, pgid)
		}
	}
}

// TestMCPS2OversizeKillsServer: a >64MiB message fails the call closed and the
// server process is killed — no orphan, no hang.
func TestMCPS2OversizeKillsServer(t *testing.T) {
	s := spawnMockServer(t, mockServerCfg(t, "--oversize-message"))
	ctx, cancel := lifecycleCtx(t)
	defer cancel()

	if _, err := s.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	pgid := s.PGID()

	_, err := s.Client().ListTools(ctx)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected fail-closed oversize error, got: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for mcpGroupAlive(pgid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if mcpGroupAlive(pgid) {
		t.Fatal("server survived the oversize message (orphan)")
	}
	// Close after the watchdog kill is idempotent.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestMCPLifecycleStderrRing: server stderr is drained (no pipe block) and
// only the last 64 KiB are retained.
func TestMCPLifecycleStderrRing(t *testing.T) {
	s := spawnMockServer(t, mockServerCfg(t, "--stderr-spam"))
	ctx, cancel := lifecycleCtx(t)
	defer cancel()

	if _, err := s.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(s.StderrTail(), "STDERR-END") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	tail := s.StderrTail()
	if !strings.Contains(tail, "STDERR-END") {
		t.Fatal("stderr marker never arrived (pipe blocked?)")
	}
	if len(tail) > 64*1024 {
		t.Fatalf("stderr ring kept %d bytes, cap is 64KiB", len(tail))
	}
	if !strings.HasSuffix(tail, "STDERR-END\n") {
		t.Fatal("ring does not retain the tail")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
