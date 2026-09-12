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

	"nabd/internal/agent"
	"nabd/internal/provider"
)

type scriptTurn struct {
	text       string
	call       *provider.ToolCall
	err        error
	rateLimits int
}

type scriptedProvider struct {
	name  string
	turns []scriptTurn
	i     int
}

func (p *scriptedProvider) Name() string {
	if p.name == "" {
		return "script"
	}
	return p.name
}

func (p *scriptedProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 16)
	if p.i >= len(p.turns) {
		close(ch)
		return ch, errors.New("no more scripted turns")
	}
	t := p.turns[p.i]
	p.i++
	go func() {
		defer close(ch)
		if t.err != nil {
			ch <- provider.Chunk{Kind: provider.ChunkError, Err: t.err}
			return
		}
		for n := 0; n < t.rateLimits; n++ {
			ch <- provider.Chunk{
				Kind:      provider.ChunkRateLimit,
				RateLimit: &provider.RateLimitInfo{Code: 429, Attempt: n + 1},
			}
		}
		if t.rateLimits > 0 && t.text == "" && t.call == nil {
			return
		}
		if t.text != "" {
			ch <- provider.Chunk{Kind: provider.ChunkText, Text: t.text}
		}
		if t.call != nil {
			ch <- provider.Chunk{Kind: provider.ChunkToolCall, Call: t.call}
			ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "tool_use"}
			return
		}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "end_turn"}
	}()
	return ch, nil
}

func writeCall(path, content string) *provider.ToolCall {
	raw, _ := json.Marshal(map[string]string{"path": path, "content": content})
	return &provider.ToolCall{ID: "c1", Name: "write_file", Input: raw}
}

func runHL(t *testing.T, cfg headlessConfig) (int, string, string) {
	t.Helper()
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(proj)
	var stdout, stderr bytes.Buffer
	cfg.stdout = &stdout
	cfg.stderr = &stderr
	if cfg.sessDir == "" {
		cfg.sessDir = t.TempDir()
	}
	if cfg.mode == "" {
		cfg.mode = permDeny
	}
	code := runHeadless(cfg)
	return code, stdout.String(), stderr.String()
}

func TestHeadlessPrintsAnswerNoANSI(t *testing.T) {
	want := "hello from nabd"
	code, out, errOut := runHL(t, headlessConfig{
		prompt:   "say hi",
		provider: &scriptedProvider{turns: []scriptTurn{{text: want}}},
	})
	if code != exitSettled {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if out != want {
		t.Fatalf("stdout %q, want %q", out, want)
	}
	if strings.ContainsRune(out, '\x1b') {
		t.Fatalf("stdout contains ANSI: %q", out)
	}
	if !strings.Contains(errOut, "session:") {
		t.Fatalf("session path must go to stderr, got %q", errOut)
	}
}

func TestHeadlessStdinPrompt(t *testing.T) {
	code, out, _ := runHL(t, headlessConfig{
		prompt:   "-",
		stdin:    strings.NewReader("from stdin\n"),
		provider: &scriptedProvider{turns: []scriptTurn{{text: "ok"}}},
	})
	if code != exitSettled || out != "ok" {
		t.Fatalf("exit %d stdout %q", code, out)
	}
}

func TestHeadlessJSONEmitsJournal(t *testing.T) {
	code, out, _ := runHL(t, headlessConfig{
		prompt:   "hi",
		json:     true,
		provider: &scriptedProvider{turns: []scriptTurn{{text: "answer"}}},
	})
	if code != exitSettled {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(out, "answer") && !strings.Contains(out, "\"type\"") {
		// answer may appear inside a text_delta JSON line; require JSONL
	}
	var sawUser, sawDelta bool
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var e agent.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("not journal JSONL %q: %v", line, err)
		}
		switch e.Type {
		case agent.UserMsg:
			sawUser = true
		case agent.TextDelta:
			sawDelta = true
		}
	}
	if !sawUser || !sawDelta {
		t.Fatalf("jsonl missing events user=%v delta=%v\n%s", sawUser, sawDelta, out)
	}
}

func TestHeadlessDenyDoesNotKillRun(t *testing.T) {
	code, out, _ := runHL(t, headlessConfig{
		prompt: "write then talk",
		mode:   permDeny,
		provider: &scriptedProvider{turns: []scriptTurn{
			{call: writeCall("x.txt", "nope")},
			{text: "refused and continued"},
		}},
	})
	if code != exitSettled {
		t.Fatalf("exit %d, want 0 (denial must not kill the run)", code)
	}
	if out != "refused and continued" {
		t.Fatalf("stdout %q", out)
	}
}

func TestHeadlessPermissionStuck(t *testing.T) {
	code, _, _ := runHL(t, headlessConfig{
		prompt: "write",
		mode:   permDeny,
		provider: &scriptedProvider{turns: []scriptTurn{
			{call: writeCall("x.txt", "nope")},
			{text: ""},
		}},
	})
	if code != exitPermStuck {
		t.Fatalf("exit %d, want %d", code, exitPermStuck)
	}
}

func TestHeadlessMaxTurns(t *testing.T) {
	code, _, _ := runHL(t, headlessConfig{
		prompt:   "loop",
		maxTurns: 1,
		provider: &scriptedProvider{turns: []scriptTurn{
			{call: writeCall("x.txt", "nope")},
		}},
	})
	if code != exitMaxTurns {
		t.Fatalf("exit %d, want %d", code, exitMaxTurns)
	}
}

func TestHeadlessProviderError(t *testing.T) {
	code, _, _ := runHL(t, headlessConfig{
		prompt:   "fail",
		provider: &scriptedProvider{turns: []scriptTurn{{err: errors.New("boom")}}},
	})
	if code != exitError {
		t.Fatalf("exit %d, want %d", code, exitError)
	}
}

func TestHeadlessRateLimitBudget(t *testing.T) {
	code, _, _ := runHL(t, headlessConfig{
		prompt:   "rl",
		provider: &scriptedProvider{turns: []scriptTurn{{rateLimits: 3}}},
	})
	if code != exitRateLimit {
		t.Fatalf("exit %d, want %d", code, exitRateLimit)
	}
}

func TestParsePermMode(t *testing.T) {
	m, err := parsePermMode("allow-reads")
	if err != nil || m != permAllowReads {
		t.Fatalf("got %q %v", m, err)
	}
	if _, err := parsePermMode("yolo"); err == nil {
		t.Fatal("yolo must be rejected")
	}
}

func TestMapHeadlessExit(t *testing.T) {
	if mapHeadlessExit(nil) != 0 {
		t.Fatal()
	}
	if mapHeadlessExit(agent.ErrMaxTurns) != 2 {
		t.Fatal()
	}
	if mapHeadlessExit(agent.ErrRateLimitBudget) != 3 {
		t.Fatal()
	}
	if mapHeadlessExit(errPermissionStuck) != 4 {
		t.Fatal()
	}
	if mapHeadlessExit(errInterrupted) != 130 {
		t.Fatal()
	}
	if mapHeadlessExit(errors.New("x")) != 1 {
		t.Fatal()
	}
}
