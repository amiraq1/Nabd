package mcp

// Phase 1 ID -> test mapping (ADR-0003 P9):
//   MCP-SEC-21  TestMCPSec21ProtocolPin
//   MCP-SEC-22  TestMCPSec22CapabilityRefusal
// (extra)       TestClientSendsRequestedVersion, TestClientToolsList,
//               TestClientToolsListCapExceeded, TestClientToolsCall,
//               TestClientPingReply, TestClientMessageTooLarge,
//               TestClientMalformedMessage, TestNegotiateVersion

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func mockServerPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "scripts", "mcp", "mock", "mock_server.py")
}

type mockProc struct {
	client *Client
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *bytes.Buffer
}

func startMock(t *testing.T, notify func(Notification), args ...string) *mockProc {
	t.Helper()
	cmd := exec.Command("python3", append([]string{mockServerPath(t)}, args...)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return &mockProc{client: NewClient(stdout, stdin, notify), cmd: cmd, stdin: stdin, stderr: stderr}
}

func (m *mockProc) close(t *testing.T) {
	t.Helper()
	m.stdin.Close()
	m.client.Close()
	done := make(chan error, 1)
	go func() { done <- m.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("mock exited: %v\nstderr:\n%s", err, m.stderr.String())
		}
	case <-time.After(15 * time.Second):
		m.cmd.Process.Kill()
		t.Fatalf("mock did not exit\nstderr:\n%s", m.stderr.String())
	}
}

func notifyChannel() (func(Notification), chan string) {
	ch := make(chan string, 16)
	return func(n Notification) { ch <- n.Method }, ch
}

func waitNotification(t *testing.T, ch chan string, want string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("got notification %q, want %q", got, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for notification %q", want)
	}
}

func bgContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 20*time.Second)
}

// TestMCPSec21ProtocolPin: the client requests 2026-07-28 and accepts only a
// version older-or-equal within the verified list (ADR-0003 v6).
func TestMCPSec21ProtocolPin(t *testing.T) {
	cases := []struct {
		serverVersion  string
		wantErr        bool
		wantNegotiated string
	}{
		{"2024-11-05", false, "2024-11-05"},
		{"2025-11-25", false, "2025-11-25"},
		{"2026-07-28", false, "2026-07-28"},
		{"2027-01-01", true, ""}, // newer than requested: refused
		{"2026-07-29", true, ""}, // newer than requested: refused
		{"2025-06-18", true, ""}, // older but unverified: refused (documented)
		{"2025-03-26", true, ""}, // older but unverified: refused (documented)
		{"bogus", true, ""},      // malformed: refused
	}
	for _, tc := range cases {
		t.Run(tc.serverVersion, func(t *testing.T) {
			m := startMock(t, nil, "--protocol-version", tc.serverVersion)
			ctx, cancel := bgContext(t)
			defer cancel()
			negotiated, err := m.client.Initialize(ctx)
			m.stdin.Close()
			m.client.Close()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected refusal for version %q", tc.serverVersion)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if negotiated != tc.wantNegotiated {
					t.Fatalf("negotiated = %q, want %q", negotiated, tc.wantNegotiated)
				}
			}
			m.close(t)
		})
	}
}

// TestClientSendsRequestedVersion: the initialize request itself carries
// protocolVersion 2026-07-28 (checked byte-level over an in-memory pipe).
func TestClientSendsRequestedVersion(t *testing.T) {
	clientR, serverW := net.Pipe()
	serverR, clientW := net.Pipe()
	defer serverR.Close()
	defer serverW.Close()
	sent := make(chan string, 1)
	go func() {
		defer serverW.Close()
		br := bufio.NewReader(serverR)
		line, err := br.ReadBytes('\n')
		if err != nil {
			sent <- ""
			return
		}
		var req struct {
			Method string  `json:"method"`
			ID     *uint64 `json:"id"`
			Params struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"params"`
		}
		if err := json.Unmarshal(line, &req); err != nil || req.Method != "initialize" {
			sent <- ""
			return
		}
		sent <- req.Params.ProtocolVersion
		var id uint64
		if req.ID != nil {
			id = *req.ID
		}
		fmt.Fprintf(serverW, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2024-11-05","capabilities":{}}}`+"\n", id)
		br.ReadBytes('\n') // notifications/initialized
	}()
	client := NewClient(clientR, clientW, nil)
	defer client.Close()
	ctx, cancel := bgContext(t)
	defer cancel()
	if _, err := client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	select {
	case v := <-sent:
		if v != "2026-07-28" {
			t.Fatalf("client requested protocolVersion %q, want 2026-07-28", v)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server side saw no initialize request")
	}
}

// TestMCPSec22CapabilityRefusal: a server-initiated method the client does not
// implement gets JSON-RPC -32601, never action.
func TestMCPSec22CapabilityRefusal(t *testing.T) {
	notify, ch := notifyChannel()
	m := startMock(t, notify, "--server-request", "roots/list")
	ctx, cancel := bgContext(t)
	defer cancel()
	if _, err := m.client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	waitNotification(t, ch, "test/refusal-validated")
	m.close(t)
}

func TestMCPSec22CapabilityRefusalSampling(t *testing.T) {
	notify, ch := notifyChannel()
	m := startMock(t, notify, "--server-request", "sampling/createMessage")
	ctx, cancel := bgContext(t)
	defer cancel()
	if _, err := m.client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	waitNotification(t, ch, "test/refusal-validated")
	m.close(t)
}

// TestClientPingReply: the client answers a server ping with an empty result.
func TestClientPingReply(t *testing.T) {
	notify, ch := notifyChannel()
	m := startMock(t, notify, "--server-ping")
	ctx, cancel := bgContext(t)
	defer cancel()
	if _, err := m.client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	waitNotification(t, ch, "test/ping-validated")
	m.close(t)
}

func TestClientToolsList(t *testing.T) {
	m := startMock(t, nil, "--tools", "3")
	ctx, cancel := bgContext(t)
	defer cancel()
	if _, err := m.client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	tools, err := m.client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("got %d tools, want 3", len(tools))
	}
	for i, tool := range tools {
		if want := fmt.Sprintf("tool_%d", i); tool.Name != want {
			t.Errorf("tool %d name = %q, want %q", i, tool.Name, want)
		}
	}
	m.close(t)
}

func TestClientToolsListCapExceeded(t *testing.T) {
	m := startMock(t, nil, "--tools", "2000")
	ctx, cancel := bgContext(t)
	defer cancel()
	if _, err := m.client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	_, err := m.client.ListTools(ctx)
	if err == nil {
		t.Fatal("expected refusal for oversized tools/list")
	}
	if !strings.Contains(err.Error(), "1024") {
		t.Fatalf("error should name the cap, got: %v", err)
	}
	m.close(t)
}

func TestClientToolsCall(t *testing.T) {
	m := startMock(t, nil)
	ctx, cancel := bgContext(t)
	defer cancel()
	if _, err := m.client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	res, err := m.client.CallTool(ctx, "tool_0", map[string]string{"q": "1"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatal("IsError set for successful call")
	}
	if len(res.Content) != 1 || res.Content[0].Text != "called:tool_0" {
		t.Fatalf("unexpected content: %+v", res.Content)
	}
	m.close(t)
}

// TestClientMessageTooLarge: a >64MiB line fails closed instead of hanging or
// exhausting memory.
func TestClientMessageTooLarge(t *testing.T) {
	big := make([]byte, MaxMessageBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	big = append(big, '\n')
	client := NewClient(bytes.NewReader(big), io.Discard, nil)
	err := client.Close()
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected oversize refusal, got: %v", err)
	}
}

// TestClientMalformedMessage: garbage on stdout fails the client closed.
func TestClientMalformedMessage(t *testing.T) {
	client := NewClient(strings.NewReader("this is not json\n"), io.Discard, nil)
	err := client.Close()
	if err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("expected malformed-message refusal, got: %v", err)
	}
}

func TestNegotiateVersion(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"2024-11-05", "2024-11-05", false},
		{"2025-11-25", "2025-11-25", false},
		{"2026-07-28", "2026-07-28", false},
		{"2027-01-01", "", true},
		{"2026-07-29", "", true},
		{"2025-06-18", "", true},
		{"2025-03-26", "", true},
		{"", "", true},
		{"bogus", "", true},
	}
	for _, tc := range cases {
		got, err := negotiateVersion(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("negotiateVersion(%q): expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("negotiateVersion(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("negotiateVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
