package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/perm"
	"nabd/internal/provider"
)

type wireTestProvider struct {
	turns []scriptTurn
	reqs  []provider.Request
	i     int
}

func (p *wireTestProvider) Name() string { return "wire-test" }

func (p *wireTestProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.reqs = append(p.reqs, req)
	ch := make(chan provider.Chunk, 16)
	if p.i >= len(p.turns) {
		close(ch)
		return ch, errors.New("no more turns")
	}
	t := p.turns[p.i]
	p.i++
	go func() {
		defer close(ch)
		if t.call != nil {
			ch <- provider.Chunk{Kind: provider.ChunkToolCall, Call: t.call}
			ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "tool_use"}
			return
		}
		if t.text != "" {
			ch <- provider.Chunk{Kind: provider.ChunkText, Text: t.text}
		}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "end_turn"}
	}()
	return ch, nil
}

// TestInteractiveSessionWiresPathRule proves that newInteractiveSession calls
// wirePathRule on the production construction path. Deleting wirePathRule from
// session.go causes this test to fail: CheckRead returns Allow instead of Deny,
// and read_file hands over the secret bytes.
func TestInteractiveSessionWiresPathRule(t *testing.T) {
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, ".gitignore"), []byte("secrets.env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "secrets.env"), []byte("token=whatever\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(proj)

	s, err := newInteractiveSession(&wireTestProvider{})
	if err != nil {
		t.Fatalf("newInteractiveSession failed: %v", err)
	}

	// 1. Policy assertion
	v, why := s.pol.CheckRead("secrets.env")
	if v != perm.Deny {
		t.Fatalf("interactive session: pol.CheckRead(\"secrets.env\") = %v (want Deny), why=%q", v, why)
	}

	// 2. Behavioral assertion through registry
	raw := json.RawMessage(`{"path":"secrets.env"}`)
	out, ok, rerr := s.reg.Run(context.Background(), provider.ToolCall{Name: "read_file", Input: raw})
	if ok || rerr == nil {
		t.Fatalf("interactive session: read_file on ignored path must be refused, got ok=%v err=%v out=%q", ok, rerr, out)
	}
	if !strings.Contains(rerr.Error(), "excluded by the session .gitignore") {
		t.Fatalf("interactive session: refusal must mention session .gitignore, got: %v", rerr)
	}
	if strings.Contains(out+rerr.Error(), "token=whatever") {
		t.Fatalf("interactive session: refused read leaked secret bytes: out=%q err=%v", out, rerr)
	}
}

// TestHeadlessSessionWiresPathRule proves that runHeadlessErr calls wirePathRule
// on the production headless execution path.
//
// Note: runHeadlessErr constructs and runs the session to completion in one
// monolithic execution, so an intermediate session object is not exposed.
// This test asserts on the end-to-end refusal behavior through the runner:
// read_file must be refused with "excluded by the session .gitignore", and no
// secret bytes may leak into the provider turn or output.
// Deleting wirePathRule from headless.go causes this test to fail: read_file
// succeeds and leaks "1|token=whatever\n".
func TestHeadlessSessionWiresPathRule(t *testing.T) {
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, ".gitignore"), []byte("secrets.env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "secrets.env"), []byte("token=whatever\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(proj)

	raw := json.RawMessage(`{"path":"secrets.env"}`)
	prov := &wireTestProvider{
		turns: []scriptTurn{
			{call: &provider.ToolCall{ID: "c1", Name: "read_file", Input: raw}},
			{text: "finished"},
		},
	}

	var stdout, stderr bytes.Buffer
	cfg := headlessConfig{
		prompt:   "read secrets",
		mode:     perm.ModeDeny,
		sessDir:  t.TempDir(),
		provider: prov,
		stdout:   &stdout,
		stderr:   &stderr,
	}

	code := runHeadless(cfg)
	if code != exitSettled {
		t.Fatalf("headless exit code = %d, stderr=%q", code, stderr.String())
	}

	if len(prov.reqs) < 2 {
		t.Fatalf("expected at least 2 requests to provider, got %d", len(prov.reqs))
	}
	req := prov.reqs[1]
	var foundResult *provider.ToolResult
	for _, m := range req.Messages {
		for _, tr := range m.ToolResults {
			if tr.ID == "c1" {
				res := tr
				foundResult = &res
				break
			}
		}
	}
	if foundResult == nil {
		t.Fatalf("tool result for c1 not found in turn 2 messages: %+v", req.Messages)
	}
	if !foundResult.IsErr {
		t.Fatalf("headless session: read_file on ignored path succeeded (wirePathRule missing in headless.go): isErr=false out=%q", foundResult.Output)
	}
	if !strings.Contains(foundResult.Output, "excluded by the session .gitignore") {
		t.Fatalf("headless session: tool result must mention exclusion, got: %q", foundResult.Output)
	}
	if strings.Contains(foundResult.Output, "token=whatever") {
		t.Fatalf("headless session: tool result leaked content: %q", foundResult.Output)
	}
}
