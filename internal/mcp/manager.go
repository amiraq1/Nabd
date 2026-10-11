package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Manager owns MCP servers: lazy spawn, per-server concurrency caps, call
// timeouts, approvals, and the journal. It is the production implementation
// behind the tools.MCPBackend interface (adapted in cmd/ag so neither
// package imports the other).
type Manager struct {
	mu        sync.Mutex
	servers   map[string]*managedServer
	approvals *Store
	journal   *Journal
}

type managedServer struct {
	id              string
	cfg             ServerConfig
	mu              sync.Mutex
	server          *Server
	client          *Client
	tools           map[string]ToolInfo
	exec            ExecIdentity
	protocolVersion string
	sem             chan struct{}
	maxParallel     int
	callTimeout     time.Duration
	activeCalls     int // in-flight CallTool calls, for timeout accounting
}

// ToolInfo is one server tool with its local classification and fingerprint.
type ToolInfo struct {
	Server         string
	Name           string
	Description    string
	InputSchema    json.RawMessage
	Raw            json.RawMessage
	Fingerprint    string
	Classification string // local config: "read-only" | "write" | "execute"
}

const (
	defaultMaxParallel = 4
	minMaxParallel     = 1
	maxMaxParallel     = 16

	handshakeTimeout   = 15 * time.Second
	defaultCallTimeout = 120 * time.Second
	maxCallTimeout     = 600 * time.Second

	// cancelGrace: after notifications/cancelled, how long a timed-out
	// server gets before killGroup.
	cancelGrace = 5 * time.Second
)

func clampParallel(n int) int {
	if n <= 0 {
		return defaultMaxParallel
	}
	if n < minMaxParallel {
		return minMaxParallel
	}
	if n > maxMaxParallel {
		return maxMaxParallel
	}
	return n
}

func callTimeoutFor(cfg ServerConfig) time.Duration {
	if cfg.CallTimeoutMs <= 0 {
		return defaultCallTimeout
	}
	d := time.Duration(cfg.CallTimeoutMs) * time.Millisecond
	if d > maxCallTimeout {
		return maxCallTimeout
	}
	return d
}

// effectiveClassification applies the local classification with the
// capture_*/observe_* minimum: such tools are at least Mutating ("write"),
// because driving a browser or observing processes has external effects.
func effectiveClassification(toolName string, c Classification) string {
	s := string(c)
	if s == "read-only" && (strings.HasPrefix(toolName, "capture_") || strings.HasPrefix(toolName, "observe_")) {
		return "write"
	}
	return s
}

// NewManager builds a Manager without starting any server (MCP-SEC-15: lazy
// startup). An empty approvalsPath selects DefaultApprovalsPath; empty
// journalPath/journalKeyPath DISABLE the journal (they do not select
// defaults) — used by tests.
func NewManager(cfg Config, approvalsPath, journalPath, journalKeyPath string) (*Manager, error) {
	if approvalsPath == "" {
		approvalsPath = DefaultApprovalsPath()
	}
	approvals, err := OpenStore(approvalsPath)
	if err != nil {
		return nil, err
	}
	journal, err := OpenJournal(journalPath, journalKeyPath)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		servers:   map[string]*managedServer{},
		approvals: approvals,
		journal:   journal,
	}
	for id, scfg := range cfg.Servers {
		if !scfg.Enabled {
			continue
		}
		mp := clampParallel(scfg.MaxParallelCalls)
		m.servers[id] = &managedServer{
			id:          id,
			cfg:         scfg,
			tools:       map[string]ToolInfo{},
			sem:         make(chan struct{}, mp),
			maxParallel: mp,
			callTimeout: callTimeoutFor(scfg),
		}
	}
	return m, nil
}

// HasServer reports whether id is a configured, enabled server.
func (m *Manager) HasServer(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.servers[id]
	return ok
}

// ensureServer spawns the server on first use and returns it. A dead server
// is re-spawned lazily with a full re-handshake and fingerprint re-verification.
func (m *Manager) ensureServer(ctx context.Context, id string) (*managedServer, error) {
	m.mu.Lock()
	ms, ok := m.servers[id]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("mcp: unknown server %q", id)
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if ms.server != nil && ms.client.Err() == nil {
		return ms, nil
	}
	// (Re)spawn with a full handshake.
	hctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	s, err := Spawn(id, ms.cfg, m.notify(id))
	if err != nil {
		return nil, err
	}
	ver, err := s.Initialize(hctx)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("mcp: server %q handshake: %w", id, err)
	}
	tools, err := s.Client().ListTools(hctx)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("mcp: server %q tools/list: %w", id, err)
	}
	envNames := make([]string, 0, len(ms.cfg.Env))
	for name := range ms.cfg.Env {
		envNames = append(envNames, name)
	}
	sort.Strings(envNames)
	exec, err := ComputeExecIdentity(s.ExecPath(), ms.cfg.Args, envNames)
	if err != nil {
		s.Close()
		return nil, err
	}
	infos := map[string]ToolInfo{}
	for _, t := range tools {
		raw := t.Raw
		if len(raw) == 0 {
			continue
		}
		fp, err := FingerprintTool(id, raw)
		if err != nil {
			continue
		}
		class := effectiveClassification(t.Name, ms.cfg.Tools[t.Name])
		infos[t.Name] = ToolInfo{
			Server:         id,
			Name:           t.Name,
			Description:    t.Description,
			InputSchema:    t.InputSchema,
			Raw:            raw,
			Fingerprint:    fp,
			Classification: class,
		}
	}
	ms.server, ms.client = s, s.Client()
	ms.tools, ms.exec, ms.protocolVersion = infos, exec, ver
	m.journal.Append(JournalEvent{
		Kind: "server_start", Server: id, ProtocolVersion: ver, Status: "ok",
	})
	return ms, nil
}

// notify handles server-initiated notifications. The callback runs inside
// the client's read loop, so it must never block on the client: refreshes
// are dispatched asynchronously.
func (m *Manager) notify(id string) func(Notification) {
	return func(n Notification) {
		if n.Method == "notifications/tools_list_changed" {
			go m.refreshTools(id)
		}
	}
}

// refreshTools re-lists tools after a tools_list_changed notification and
// re-verifies fingerprints; changed fingerprints automatically invalidate
// approvals via Store.Check (MCP-SEC-23).
func (m *Manager) refreshTools(id string) {
	m.mu.Lock()
	ms, ok := m.servers[id]
	m.mu.Unlock()
	if !ok {
		return
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if ms.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	defer cancel()
	tools, err := ms.client.ListTools(ctx)
	if err != nil {
		return // keep the old list on failure
	}
	infos := map[string]ToolInfo{}
	for _, t := range tools {
		raw := t.Raw
		if len(raw) == 0 {
			continue
		}
		fp, err := FingerprintTool(id, raw)
		if err != nil {
			continue
		}
		infos[t.Name] = ToolInfo{
			Server:         id,
			Name:           t.Name,
			Description:    t.Description,
			InputSchema:    t.InputSchema,
			Raw:            raw,
			Fingerprint:    fp,
			Classification: effectiveClassification(t.Name, ms.cfg.Tools[t.Name]),
		}
	}
	ms.tools = infos
}

// Describe resolves (server, tool). Unknown tools, unclassified tools, and
// the reserved adapter name are refused (MCP-SEC-04/08).
func (m *Manager) Describe(server, tool string) (ToolInfo, error) {
	if tool == "mcp_tool" {
		return ToolInfo{}, errors.New(`mcp: "mcp_tool" is a reserved name`)
	}
	ms, err := m.ensureServer(context.Background(), server)
	if err != nil {
		return ToolInfo{}, err
	}
	ms.mu.Lock()
	info, ok := ms.tools[tool]
	ms.mu.Unlock()
	if !ok {
		return ToolInfo{}, fmt.Errorf("mcp: unknown tool %s/%s", server, tool)
	}
	if info.Classification == "" {
		return ToolInfo{}, fmt.Errorf("mcp: tool %s/%s has no local classification", server, tool)
	}
	return info, nil
}

// IsApproved checks the approval store for this exact binding.
func (m *Manager) IsApproved(info ToolInfo) bool {
	m.mu.Lock()
	ms, ok := m.servers[info.Server]
	m.mu.Unlock()
	if !ok {
		return false
	}
	ms.mu.Lock()
	exec := ms.exec
	ms.mu.Unlock()
	return m.approvals.Check(info.Server, info.Name, info.Fingerprint, exec)
}

// Approve records the first-approval for this binding.
func (m *Manager) Approve(info ToolInfo) error {
	m.mu.Lock()
	ms, ok := m.servers[info.Server]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("mcp: unknown server %q", info.Server)
	}
	ms.mu.Lock()
	exec := ms.exec
	ms.mu.Unlock()
	if err := m.approvals.Record(Approval{
		Server:         info.Server,
		Tool:           info.Name,
		Fingerprint:    info.Fingerprint,
		Classification: info.Classification,
		Exec:           exec,
	}); err != nil {
		return err
	}
	m.journal.Append(JournalEvent{
		Kind: "approval", Server: info.Server, Tool: info.Name,
		Decision: "approved", Status: "ok",
	})
	return nil
}

// ApprovalText builds the first-approval content locally — never free text
// from the server (ADR-0003 §6).
func (m *Manager) ApprovalText(info ToolInfo) string {
	m.mu.Lock()
	ms, ok := m.servers[info.Server]
	m.mu.Unlock()
	var exec ExecIdentity
	if ok {
		ms.mu.Lock()
		exec = ms.exec
		ms.mu.Unlock()
	}
	var b strings.Builder
	b.WriteString("MCP first approval — built locally, no server text\n")
	fmt.Fprintf(&b, "server: %s\n", info.Server)
	fmt.Fprintf(&b, "tool: %s\n", info.Name)
	// No server description here: the first approval shows only locally-built
	// content. Server text is untrusted and never part of a trust decision.
	fmt.Fprintf(&b, "classification (local config): %s\n", info.Classification)
	fmt.Fprintf(&b, "fingerprint: %s\n", info.Fingerprint)
	fmt.Fprintf(&b, "executable: %s %s\n", exec.Path, strings.Join(exec.Args, " "))
	fmt.Fprintf(&b, "env scope: %s\n", strings.Join(exec.EnvNames, ", "))
	fmt.Fprintf(&b, "binary sha256: %s\n", exec.BinarySHA256)
	b.WriteString("WARNING: the server runs as your user with your privileges. " +
		"The server is untrusted by default; its output is data, never instructions.")
	return b.String()
}

// Call invokes a tool with the per-server concurrency cap and call timeout.
// Excess calls are refused immediately, never queued (MCP-SEC-24).
// tools/call is never retried (MCP-SEC-10).
func (m *Manager) Call(ctx context.Context, server, tool string, args json.RawMessage) (string, bool, error) {
	ms, err := m.ensureServer(ctx, server)
	if err != nil {
		return "", false, err
	}
	select {
	case ms.sem <- struct{}{}:
		defer func() { <-ms.sem }()
	default:
		return "", false, fmt.Errorf("mcp: server %q at concurrency limit (%d); refusing, not queueing", server, ms.maxParallel)
	}

	// Active-call accounting: a timeout kills the server, aborting every
	// in-flight call; the count is journaled with the abort (MCP-SEC-11/12).
	ms.mu.Lock()
	ms.activeCalls++
	ms.mu.Unlock()
	defer func() {
		ms.mu.Lock()
		ms.activeCalls--
		ms.mu.Unlock()
	}()

	cctx, cancel := context.WithTimeout(ctx, ms.callTimeout)
	defer cancel()
	start := time.Now()
	res, err := ms.client.CallTool(cctx, tool, args)
	dur := time.Since(start)

	var output string
	var isError bool
	if err == nil {
		isError = res.IsError
		var b strings.Builder
		for _, item := range res.Content {
			b.WriteString(item.Text)
		}
		output = b.String()
	}

	ev := JournalEvent{
		Kind: "call", Server: server, Tool: tool,
		DurationMs: dur.Milliseconds(), OutputBytes: len(output),
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		ev.Status = "timeout"
		m.journal.Append(ev)
		m.onCallTimeout(ms)
		return "", false, fmt.Errorf("mcp: call %s/%s timed out after %s; server restarted", server, tool, ms.callTimeout)
	case err != nil:
		ev.Status = "error"
		m.journal.Append(ev)
		return "", false, err
	default:
		ev.Status = "ok"
		if m.journal != nil {
			ev.ArgsHMAC = m.journal.HMACArgs(args)
		}
		m.journal.Append(ev)
		return output, isError, nil
	}
}

// onCallTimeout gives the server cancelGrace to react to
// notifications/cancelled, then kills the group and forces a lazy re-spawn
// with a full re-handshake (MCP-SEC-11/12). Killing the server aborts every
// in-flight call on it; the aborted count and the reason are journaled.
func (m *Manager) onCallTimeout(ms *managedServer) {
	ms.mu.Lock()
	s := ms.server
	aborted := ms.activeCalls
	ms.mu.Unlock()
	if s == nil {
		return
	}
	deadline := time.Now().Add(cancelGrace)
	for time.Now().Before(deadline) && mcpGroupAlive(s.PGID()) {
		time.Sleep(100 * time.Millisecond)
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if ms.server == s {
		if mcpGroupAlive(s.PGID()) {
			s.Kill()
		}
		ms.server, ms.client = nil, nil
	}
	m.journal.Append(JournalEvent{
		Kind: "server_timeout", Server: ms.id,
		Decision: "killed", Status: "ok",
		// aborted counts the in-flight calls killed with the server,
		// including the one whose timeout triggered this.
		AbortedCalls: aborted,
	})
}

// Close shuts down all servers.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ms := range m.servers {
		ms.mu.Lock()
		if ms.server != nil {
			ms.server.Close()
			ms.server, ms.client = nil, nil
		}
		ms.mu.Unlock()
	}
	return nil
}
