package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Client is a JSON-RPC 2.0 MCP client over newline-delimited stdio.
//
// It owns no process: the caller provides the streams (server stdout as the
// reader, server stdin as the writer). Process lifecycle — spawn, environment,
// termination — is Phase 2. All writes are serialized; inbound messages are
// demultiplexed on a background read loop started by NewClient and stopped by
// Close.
//
// Inbound server requests are answered, never obeyed: ping gets an empty
// result, anything else gets JSON-RPC -32601 (MCP-SEC-22). Inbound
// notifications go to the notify callback, if set.
type Client struct {
	w   io.Writer
	wmu sync.Mutex

	pmu     sync.Mutex
	pending map[uint64]chan Response
	next    uint64

	notify func(Notification)

	reader *bufio.Reader
	done   chan struct{} // closed when the read loop ends
	closed atomic.Bool

	closeOnce sync.Once
	wg        sync.WaitGroup

	errmu sync.Mutex
	rerr  error // terminal read-loop error; nil on clean shutdown
}

// NewClient starts a client over r (server stdout) and w (server stdin).
// notify receives server notifications; nil drops them.
func NewClient(r io.Reader, w io.Writer, notify func(Notification)) *Client {
	c := &Client{
		w:       w,
		pending: make(map[uint64]chan Response),
		notify:  notify,
		reader:  bufio.NewReader(r),
		done:    make(chan struct{}),
	}
	c.wg.Add(1)
	go c.readLoop()
	return c
}

// Initialize performs the MCP handshake: it sends initialize requesting
// ProtocolVersionRequest, negotiates the version (MCP-SEC-21), then sends
// notifications/initialized. It returns the negotiated version, which the
// caller records in the Journal (ADR-0003 v6).
func (c *Client) Initialize(ctx context.Context) (string, error) {
	result, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersionRequest,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "nabd"},
	})
	if err != nil {
		return "", err
	}
	var ir InitializeResult
	if err := json.Unmarshal(result, &ir); err != nil {
		return "", fmt.Errorf("mcp: bad initialize result: %w", err)
	}
	if ir.ProtocolVersion == "" {
		return "", errors.New("mcp: initialize result missing protocolVersion")
	}
	negotiated, err := negotiateVersion(ir.ProtocolVersion)
	if err != nil {
		return "", err
	}
	if err := c.notifyMessage("notifications/initialized", nil); err != nil {
		return "", err
	}
	return negotiated, nil
}

// ListTools returns the tools advertised by the server. A list larger than
// MaxToolsListCount is refused outright (fail-closed).
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	result, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var lr struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(result, &lr); err != nil {
		return nil, fmt.Errorf("mcp: bad tools/list result: %w", err)
	}
	if len(lr.Tools) > MaxToolsListCount {
		return nil, fmt.Errorf("mcp: tools/list returned %d tools, cap is %d: refusing", len(lr.Tools), MaxToolsListCount)
	}
	return lr.Tools, nil
}

// CallTool invokes a server tool by name with the given arguments.
func (c *Client) CallTool(ctx context.Context, name string, args any) (*CallToolResult, error) {
	result, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return nil, err
	}
	var cr CallToolResult
	if err := json.Unmarshal(result, &cr); err != nil {
		return nil, fmt.Errorf("mcp: bad tools/call result: %w", err)
	}
	return &cr, nil
}

// Close shuts the client down: no further calls, pending calls are aborted,
// and the read loop is joined. It returns the read loop's terminal error,
// or nil on a clean shutdown.
func (c *Client) Close() error {
	c.closed.Store(true)
	c.closeOnce.Do(func() { close(c.done) })
	c.abortPending()
	c.wg.Wait()
	c.errmu.Lock()
	defer c.errmu.Unlock()
	return c.rerr
}

// call performs one JSON-RPC request/response round trip.
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	var p json.RawMessage
	if params != nil {
		var err error
		if p, err = json.Marshal(params); err != nil {
			return nil, err
		}
	}
	id := atomic.AddUint64(&c.next, 1)
	req := Request{JSONRPC: "2.0", ID: &id, Method: method, Params: p}

	ch := make(chan Response, 1)
	c.pmu.Lock()
	select {
	case <-c.done:
		c.pmu.Unlock()
		return nil, c.loopErr()
	default:
	}
	c.pending[id] = ch
	c.pmu.Unlock()

	if err := c.writeMessage(req); err != nil {
		c.removePending(id)
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.removePending(id)
		// Best-effort cancellation notice; the server may ignore it.
		_ = c.notifyMessage("notifications/cancelled", map[string]any{
			"requestId": id,
			"reason":    ctx.Err().Error(),
		})
		return nil, ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			return nil, c.loopErr()
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-c.done:
		return nil, c.loopErr()
	}
}

func (c *Client) notifyMessage(method string, params any) error {
	var p json.RawMessage
	if params != nil {
		var err error
		if p, err = json.Marshal(params); err != nil {
			return err
		}
	}
	return c.writeMessage(Request{JSONRPC: "2.0", Method: method, Params: p})
}

func (c *Client) writeMessage(v any) error {
	if c.closed.Load() {
		return errors.New("mcp: client closed")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.w.Write(data)
	return err
}

func (c *Client) readLoop() {
	defer c.wg.Done()
	for {
		line, err := c.readFrame()
		if err != nil {
			c.fail(err)
			return
		}
		c.dispatch(line)
	}
}

// readFrame returns the next non-blank newline-delimited message.
// A message larger than MaxMessageBytes fails the client closed.
func (c *Client) readFrame() ([]byte, error) {
	var buf bytes.Buffer
	for {
		buf.Reset()
		for {
			// ReadSlice bounds each allocation; the running total enforces the cap.
			frag, err := c.reader.ReadSlice('\n')
			if len(frag) > 0 {
				if buf.Len()+len(frag) > MaxMessageBytes+1 {
					return nil, fmt.Errorf("mcp: message exceeds %d bytes: refusing", MaxMessageBytes)
				}
				buf.Write(frag)
			}
			if err == nil {
				break
			}
			if err == bufio.ErrBufferFull {
				continue
			}
			if err == io.EOF {
				if buf.Len() == 0 {
					return nil, io.EOF
				}
				return nil, errors.New("mcp: truncated message from server (no trailing newline)")
			}
			return nil, err
		}
		line := bytes.TrimRight(buf.Bytes(), "\n")
		line = bytes.TrimRight(line, "\r")
		if len(bytes.TrimSpace(line)) == 0 {
			continue // skip blank lines
		}
		out := make([]byte, len(line))
		copy(out, line)
		return out, nil
	}
}

// dispatch routes one inbound message: responses complete pending calls,
// requests are answered (never obeyed), notifications go to the callback.
func (c *Client) dispatch(line []byte) {
	var probe struct {
		ID     *uint64 `json:"id"`
		Method string  `json:"method"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		c.fail(fmt.Errorf("mcp: malformed message from server: %w", err))
		return
	}
	if probe.Method != "" {
		if probe.ID != nil {
			c.handleRequest(probe.ID, probe.Method)
		} else if c.notify != nil {
			var n Notification
			if err := json.Unmarshal(line, &n); err == nil {
				c.notify(n)
			}
		}
		return
	}
	if probe.ID == nil {
		return // response without id: ignore
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		c.fail(fmt.Errorf("mcp: malformed response from server: %w", err))
		return
	}
	c.pmu.Lock()
	ch, ok := c.pending[*probe.ID]
	if ok {
		delete(c.pending, *probe.ID)
	}
	c.pmu.Unlock()
	if ok {
		ch <- resp
	}
}

// handleRequest answers a server-initiated request. The client implements no
// server-facing capabilities: ping gets an empty result, everything else gets
// JSON-RPC -32601 (MCP-SEC-22).
func (c *Client) handleRequest(id *uint64, method string) {
	resp := Response{JSONRPC: "2.0", ID: id}
	if method == "ping" {
		resp.Result = json.RawMessage("{}")
	} else {
		resp.Error = &RPCError{Code: -32601, Message: "Method not found: " + method}
	}
	_ = c.writeMessage(resp)
}

func (c *Client) removePending(id uint64) {
	c.pmu.Lock()
	delete(c.pending, id)
	c.pmu.Unlock()
}

func (c *Client) abortPending() {
	c.pmu.Lock()
	for id, ch := range c.pending {
		delete(c.pending, id)
		close(ch)
	}
	c.pmu.Unlock()
}

func (c *Client) fail(err error) {
	if err != nil && !errors.Is(err, io.EOF) {
		c.errmu.Lock()
		if c.rerr == nil {
			c.rerr = err
		}
		c.errmu.Unlock()
	}
	c.closed.Store(true)
	c.closeOnce.Do(func() { close(c.done) })
	c.abortPending()
}

func (c *Client) loopErr() error {
	c.errmu.Lock()
	defer c.errmu.Unlock()
	if c.rerr != nil {
		return c.rerr
	}
	return errors.New("mcp: client closed")
}
