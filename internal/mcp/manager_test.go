package mcp

// Phase 5 ID -> test mapping (ADR-0003 P9):
//   MCP-SEC-11/12 TestMCPSec11_12TimeoutOrphan
//   MCP-SEC-15    TestMCPSec15LazyStartup
//   MCP-SEC-23    TestMCPSec23ToolListChange
//   MCP-SEC-24    TestMCPSec24ParallelCap
// (extra)         TestManagerDescribeApprove, TestEffectiveClassification

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func testManagerCfg(t *testing.T, pidFile string, extraArgs ...string) Config {
	t.Helper()
	args := append([]string{mockServerPath(t)}, extraArgs...)
	if pidFile != "" {
		args = append(args, "--pid-file", pidFile)
	}
	return Config{
		Servers: map[string]ServerConfig{
			"srv": {
				Enabled:   true,
				Transport: "stdio",
				Command:   "python3",
				Args:      args,
				Tools: map[string]Classification{
					"tool_0": ClassReadOnly,
					"tool_1": ClassWrite,
				},
				MaxParallelCalls: 4,
				CallTimeoutMs:    120000,
			},
		},
	}
}

func testManager(t *testing.T, cfg Config) *Manager {
	t.Helper()
	skipOnWindows(t)
	dir := t.TempDir()
	m, err := NewManager(cfg,
		filepath.Join(dir, "approvals.json"),
		filepath.Join(dir, "journal.jsonl"),
		filepath.Join(dir, "journal.key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

// TestMCPSec15LazyStartup: NewManager spawns nothing; the first Describe
// starts the server on demand.
func TestMCPSec15LazyStartup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	m := testManager(t, testManagerCfg(t, pidFile))
	defer m.Close()

	if !m.HasServer("srv") {
		t.Fatal("configured server not known")
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatal("server spawned before first use")
	}
	if _, err := m.Describe("srv", "tool_0"); err != nil {
		t.Fatalf("Describe: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(pidFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not start on first use")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestManagerDescribeApprove(t *testing.T) {
	m := testManager(t, testManagerCfg(t, ""))
	defer m.Close()

	info, err := m.Describe("srv", "tool_0")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if info.Classification != "read-only" || info.Fingerprint == "" {
		t.Fatalf("bad descriptor: %+v", info)
	}
	if m.IsApproved(info) {
		t.Fatal("not approved yet")
	}
	if err := m.Approve(info); err != nil {
		t.Fatal(err)
	}
	if !m.IsApproved(info) {
		t.Fatal("should be approved after Approve")
	}
	text := m.ApprovalText(info)
	for _, want := range []string{"srv", "tool_0", "read-only", info.Fingerprint, "WARNING"} {
		if !strings.Contains(text, want) {
			t.Fatalf("approval text missing %q", want)
		}
	}

	// Unknown server/tool, unclassified, reserved name.
	if _, err := m.Describe("nope", "tool_0"); err == nil {
		t.Fatal("unknown server must fail")
	}
	if _, err := m.Describe("srv", "nope"); err == nil {
		t.Fatal("unknown tool must fail")
	}
	if _, err := m.Describe("srv", "mcp_tool"); err == nil {
		t.Fatal("reserved name must fail")
	}
}

func TestEffectiveClassification(t *testing.T) {
	if got := effectiveClassification("capture_browser_scenario", ClassReadOnly); got != "write" {
		t.Fatalf("capture_* must be at least write, got %q", got)
	}
	if got := effectiveClassification("observe_processes", ClassReadOnly); got != "write" {
		t.Fatalf("observe_* must be at least write, got %q", got)
	}
	if got := effectiveClassification("analyze", ClassReadOnly); got != "read-only" {
		t.Fatalf("plain tool keeps read-only, got %q", got)
	}
	if got := effectiveClassification("capture_x", ClassExecute); got != "execute" {
		t.Fatalf("explicit execute stays, got %q", got)
	}
}

// TestMCPSec24ParallelCap: the per-server cap refuses excess calls
// immediately instead of queueing them (no freeze).
func TestMCPSec24ParallelCap(t *testing.T) {
	cfg := testManagerCfg(t, "")
	cfg.Servers["srv"] = func() ServerConfig {
		sc := cfg.Servers["srv"]
		sc.MaxParallelCalls = 2
		sc.Args = append(sc.Args, "--slow-call", "2")
		return sc
	}()
	m := testManager(t, cfg)
	defer m.Close()

	// Warm up the server.
	if _, err := m.Describe("srv", "tool_0"); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	type result struct {
		err error
		dur time.Duration
	}
	results := make([]result, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _, err := m.Call(ctx, "srv", "tool_0", json.RawMessage(`{}`))
			results[i] = result{err, time.Since(start)}
		}(i)
	}
	wg.Wait()

	var refused, okCount int
	for _, r := range results {
		if r.err != nil && strings.Contains(r.err.Error(), "concurrency limit") {
			refused++
			// Refused immediately, not queued behind the slow calls.
			if r.dur > time.Second {
				t.Fatalf("refusal was not immediate: %s", r.dur)
			}
		} else if r.err == nil {
			okCount++
		} else {
			t.Fatalf("unexpected error: %v", r.err)
		}
	}
	if okCount != 2 || refused != 2 {
		t.Fatalf("want 2 ok + 2 refused, got %d ok + %d refused", okCount, refused)
	}
}

// TestMCPSec11_12TimeoutOrphan: a hung call times out, the server is killed
// (no orphan), and the next call re-spawns with a full re-handshake. The
// kill is journaled with the aborted in-flight call count.
func TestMCPSec11_12TimeoutOrphan(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	journalPath := filepath.Join(dir, "journal.jsonl")
	cfg := testManagerCfg(t, pidFile, "--slow-call", "30")
	sc := cfg.Servers["srv"]
	sc.CallTimeoutMs = 2000
	cfg.Servers["srv"] = sc
	m, err := NewManager(cfg,
		filepath.Join(dir, "approvals.json"),
		journalPath,
		filepath.Join(dir, "journal.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	_, _, err = m.Call(ctx, "srv", "tool_0", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if time.Since(start) > 30*time.Second {
		t.Fatalf("timeout handling took too long: %s", time.Since(start))
	}

	// The old server process is gone (no orphan).
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("no pid file: %v", err)
	}
	oldPid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || oldPid <= 1 {
		t.Fatalf("bad pid file content %q", raw)
	}
	deadline := time.Now().Add(15 * time.Second)
	for syscall.Kill(oldPid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if syscall.Kill(oldPid, 0) == nil {
		t.Fatalf("timed-out server (pid %d) survived: orphan", oldPid)
	}

	// Next call re-spawns transparently with a NEW process.
	os.Remove(pidFile)
	callDone := make(chan error, 1)
	go func() {
		_, _, err := m.Call(ctx, "srv", "tool_0", json.RawMessage(`{}`))
		callDone <- err
	}()
	newPid := 0
	deadline = time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if p, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && p != oldPid {
				newPid = p
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if newPid == 0 {
		t.Fatal("server was not re-spawned after timeout")
	}
	<-callDone // let the second (also timing-out) call finish

	// The kill is journaled with the aborted call count (>= 1: the timed-out
	// call itself).
	journalRaw, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	foundTimeout := false
	for _, line := range strings.Split(string(journalRaw), "\n") {
		var ev struct {
			Kind         string `json:"kind"`
			AbortedCalls int    `json:"aborted_calls"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev.Kind == "server_timeout" {
			foundTimeout = true
			if ev.AbortedCalls < 1 {
				t.Fatalf("server_timeout must report aborted calls, got %d", ev.AbortedCalls)
			}
		}
	}
	if !foundTimeout {
		t.Fatal("no server_timeout event in journal")
	}
}

// TestMCPSec23ToolListChange: a tools_list_changed notification triggers a
// re-list; new tools are unclassified (= refused) until configured.
func TestMCPSec23ToolListChange(t *testing.T) {
	cfg := testManagerCfg(t, "", "--list-changed-after", "1")
	m := testManager(t, cfg)
	defer m.Close()

	if _, err := m.Describe("srv", "tool_0"); err != nil {
		t.Fatal(err)
	}

	// Wait for the notification + re-list, then peek at the internal list
	// (Describe refuses unclassified tools by design).
	// Generous deadline: under full-suite load the mock startup and the
	// async re-list can be slow.
	deadline := time.Now().Add(45 * time.Second)
	for {
		m.mu.Lock()
		ms := m.servers["srv"]
		ms.mu.Lock()
		_, found := ms.tools["tool_new"]
		class := ""
		if info, ok := ms.tools["tool_new"]; ok {
			class = info.Classification
		}
		ms.mu.Unlock()
		m.mu.Unlock()
		if found {
			if class != "" {
				t.Fatalf("new tool should be unclassified, got %q", class)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("tool list was not refreshed after notification")
		}
		time.Sleep(200 * time.Millisecond)
	}

	// And Describe still refuses it (SEC-04).
	if _, err := m.Describe("srv", "tool_new"); err == nil {
		t.Fatal("unclassified new tool must be refused")
	}
}
