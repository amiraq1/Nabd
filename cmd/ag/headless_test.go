package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"nabd/internal/agent"
	"nabd/internal/config"
	"nabd/internal/perm"
	"nabd/internal/provider"
	"nabd/internal/snap"
	"nabd/internal/tools"
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
	if cfg.mode == perm.ModeAsk {
		cfg.mode = perm.ModeDeny
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
		mode:   perm.ModeDeny,
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
		mode:   perm.ModeDeny,
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
	m, err := perm.ParseMode("allow-reads")
	if err != nil || m != perm.ModeAllowReads {
		t.Fatalf("got %q %v", m, err)
	}
	m, err = perm.ParseMode("plan")
	if err != nil || m != perm.ModePlan {
		t.Fatalf("plan: got %q %v", m, err)
	}
	if _, err := perm.ParseMode("yolo"); err == nil {
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

type trackingProvider struct {
	called bool
}

func (p *trackingProvider) Name() string { return "tracker" }
func (p *trackingProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.called = true
	ch := make(chan provider.Chunk)
	close(ch)
	return ch, errors.New("provider called unexpectedly")
}

func TestRemovedBashKeysFailBeforeProviderOrTool(t *testing.T) {
	cases := []struct {
		key string
		val string
	}{
		{"NABD_BASH_SANDBOX", "on"},
		{"NABD_BASH_NETWORK", "deny"},
		{"NABD_BASH_RESOURCES", "limit"},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			config.ResetForTest()
			t.Cleanup(config.ResetForTest)

			t.Setenv(config.EnvVar, filepath.Join(t.TempDir(), "missing-config"))
			t.Setenv(config.V2EnvVar, "")
			t.Setenv(tc.key, tc.val)

			prov := &trackingProvider{}
			var stdout, stderr bytes.Buffer
			code := runHeadless(headlessConfig{
				prompt:   "echo hi",
				provider: prov,
				stdout:   &stdout,
				stderr:   &stderr,
				sessDir:  t.TempDir(),
			})

			if code != exitError {
				t.Fatalf("expected exit code %d, got %d", exitError, code)
			}
			if prov.called {
				t.Fatal("provider was called; expected early fail-closed abort before provider invocation")
			}
			wantSub := fmt.Sprintf("%s=%s is no longer supported: this Termux build has no bash sandbox. Remove the setting to continue", tc.key, tc.val)
			if !strings.Contains(stderr.String(), wantSub) {
				t.Fatalf("stderr = %q, want substring %q", stderr.String(), wantSub)
			}
		})
	}
}

// yoloSink records every event so the test can inspect the permission reply.
type yoloSink func(agent.Event) error

func (s yoloSink) Emit(e agent.Event) error { return s(e) }

// TestHeadlessYOLOBashDoesNotHang is the headless counterpart to the enforce
// decision. Headless has no TTY: silentAsker is the Human and it answers Deny
// without blocking. Entering the loop in ModeAsk (the only mode where a Check
// can return Ask) with YOLO on and a provider that requests bash must
// therefore complete, not wait for an answer that can never come.
func TestHeadlessYOLOBashDoesNotHang(t *testing.T) {
	root, err := tools.NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(root, sh)

	pol := perm.New(reg)
	pol.SetMode(perm.ModeAsk)
	pol.SetYOLO(true)

	prov := &scriptedProvider{turns: []scriptTurn{
		{call: &provider.ToolCall{ID: "b1", Name: "bash", Input: []byte(`{"cmd":"echo hi"}`)}},
		{text: "done"},
	}}
	loop := newSessionLoop(prov, reg, gate{pol}, silentAsker{})

	var events []agent.Event
	loop.Sink = yoloSink(func(e agent.Event) error {
		events = append(events, e)
		return nil
	})

	done := make(chan error, 1)
	go func() { done <- loop.Run(context.Background(), "run bash") }()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("headless YOLO + ModeAsk + bash hung: the silent asker must answer without blocking")
	}

	var denied bool
	for _, e := range events {
		if e.Type == agent.PermReply && e.Call != nil && e.Call.Name == "bash" && e.Decision == agent.Deny {
			denied = true
		}
	}
	if !denied {
		t.Fatal("headless YOLO did not deny bash; expected a PermReply with Decision=Deny")
	}
}

func TestHeadlessJSONAllLinesValidJSON(t *testing.T) {
	code, out, errOut := runHL(t, headlessConfig{
		prompt:   "fail",
		json:     true,
		provider: &scriptedProvider{turns: []scriptTurn{{err: errors.New("provider failure")}}},
	})
	if code != exitError {
		t.Fatalf("exit %d, want %d", code, exitError)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		t.Fatalf("expected JSONL output, got empty stdout")
	}

	var sawRunError, sawRunEnd bool
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Fatalf("line %d on stdout is not valid JSON: %q (err: %v)", i, line, err)
		}
		var ev agent.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("line %d on stdout cannot unmarshal into agent.Event: %q (err: %v)", i, line, err)
		}
		if ev.Type == agent.RunError {
			sawRunError = true
		}
		if ev.Type == agent.RunEnd {
			sawRunEnd = true
		}
	}

	if !sawRunError {
		t.Fatalf("expected run_error event on stdout JSONL")
	}
	if !sawRunEnd {
		t.Fatalf("expected run_end event on stdout JSONL")
	}

	// Verify all notes, error text, and session path went to stderr
	if !strings.Contains(errOut, "provider failure") {
		t.Errorf("stderr missing error text: %q", errOut)
	}
	if !strings.Contains(errOut, "session:") {
		t.Errorf("stderr missing 'session:' line: %q", errOut)
	}
}

func TestHeadlessRunEndReflectsFailureAfterRunError(t *testing.T) {
	code, out, _ := runHL(t, headlessConfig{
		prompt:   "fail",
		json:     true,
		provider: &scriptedProvider{turns: []scriptTurn{{err: errors.New("critical failure")}}},
	})
	if code != exitError {
		t.Fatalf("exit %d, want %d", code, exitError)
	}

	var runEndEvent *agent.Event
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var ev agent.Event
		if err := json.Unmarshal([]byte(line), &ev); err == nil && ev.Type == agent.RunEnd {
			runEndEvent = &ev
			break
		}
	}

	if runEndEvent == nil {
		t.Fatalf("run_end event was not emitted")
	}

	// Must NOT declare success ("جلسة منتهية")
	if strings.Contains(runEndEvent.Text, "جلسة منتهية") {
		t.Errorf("run_end declared normal ending despite failure: %q", runEndEvent.Text)
	}

	// Must reflect failure ("فشلت الجلسة")
	if !strings.Contains(runEndEvent.Text, "فشلت الجلسة") {
		t.Errorf("run_end missing failure indicator 'فشلت الجلسة': %q", runEndEvent.Text)
	}
}

type interruptingProvider struct{}

func (p *interruptingProvider) Name() string { return "interrupter" }

func (p *interruptingProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 1)
	// Raise SIGINT to process; signal.NotifyContext in runHeadlessErr intercepts it and cancels ctx.
	_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	<-ctx.Done()
	ch <- provider.Chunk{Kind: provider.ChunkError, Err: ctx.Err()}
	close(ch)
	return ch, nil
}

func TestHeadlessRunEndReflectsStoppedAfterInterruption(t *testing.T) {
	code, out, _ := runHL(t, headlessConfig{
		prompt:   "stop-me",
		json:     true,
		provider: &interruptingProvider{},
	})
	if code != exitInterrupted {
		t.Fatalf("exit %d, want %d", code, exitInterrupted)
	}

	var runEndEvent *agent.Event
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var ev agent.Event
		if err := json.Unmarshal([]byte(line), &ev); err == nil && ev.Type == agent.RunEnd {
			runEndEvent = &ev
			break
		}
	}

	if runEndEvent == nil {
		t.Fatalf("run_end event was not emitted")
	}

	// Must reflect stopped ("أوقفت الجلسة")
	if !strings.Contains(runEndEvent.Text, "أوقفت الجلسة") {
		t.Errorf("run_end missing stopped indicator 'أوقفت الجلسة': %q", runEndEvent.Text)
	}
	// Must NOT declare success ("جلسة منتهية") or failure ("فشلت الجلسة")
	if strings.Contains(runEndEvent.Text, "جلسة منتهية") {
		t.Errorf("run_end declared normal ending despite interruption: %q", runEndEvent.Text)
	}
	if strings.Contains(runEndEvent.Text, "فشلت الجلسة") {
		t.Errorf("run_end declared failure despite interruption: %q", runEndEvent.Text)
	}
}

func TestHeadlessRunEndNormalOnSuccessAndMaxTurns(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		code, out, _ := runHL(t, headlessConfig{
			prompt:   "hi",
			json:     true,
			provider: &scriptedProvider{turns: []scriptTurn{{text: "hello"}}},
		})
		if code != exitSettled {
			t.Fatalf("exit %d, want %d", code, exitSettled)
		}

		var runEndEvent *agent.Event
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			var ev agent.Event
			if err := json.Unmarshal([]byte(line), &ev); err == nil && ev.Type == agent.RunEnd {
				runEndEvent = &ev
				break
			}
		}
		if runEndEvent == nil {
			t.Fatalf("run_end event was not emitted")
		}
		if !strings.Contains(runEndEvent.Text, "جلسة منتهية") {
			t.Errorf("run_end missing normal ending indicator 'جلسة منتهية': %q", runEndEvent.Text)
		}
		if strings.Contains(runEndEvent.Text, "فشلت الجلسة") || strings.Contains(runEndEvent.Text, "أوقفت الجلسة") {
			t.Errorf("run_end declared failure or stopped on success: %q", runEndEvent.Text)
		}
	})

	t.Run("max_turns", func(t *testing.T) {
		code, out, _ := runHL(t, headlessConfig{
			prompt:   "loop",
			json:     true,
			maxTurns: 1,
			provider: &scriptedProvider{turns: []scriptTurn{
				{call: writeCall("x.txt", "nope")},
			}},
		})
		if code != exitMaxTurns {
			t.Fatalf("exit %d, want %d", code, exitMaxTurns)
		}

		var runEndEvent *agent.Event
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			var ev agent.Event
			if err := json.Unmarshal([]byte(line), &ev); err == nil && ev.Type == agent.RunEnd {
				runEndEvent = &ev
				break
			}
		}
		if runEndEvent == nil {
			t.Fatalf("run_end event was not emitted")
		}
		if !strings.Contains(runEndEvent.Text, "جلسة منتهية") {
			t.Errorf("run_end missing normal ending indicator 'جلسة منتهية' on max turns: %q", runEndEvent.Text)
		}
		if strings.Contains(runEndEvent.Text, "فشلت الجلسة") || strings.Contains(runEndEvent.Text, "أوقفت الجلسة") {
			t.Errorf("run_end declared failure or stopped on max turns: %q", runEndEvent.Text)
		}
	})
}
