package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"nabd/internal/agent"
	"nabd/internal/build"
	"nabd/internal/config"
	"nabd/internal/perm"
	"nabd/internal/provider"
	"nabd/internal/snap"
	"nabd/internal/store"
	"nabd/internal/tools"
)

const (
	exitSettled     = 0
	exitError       = 1
	exitMaxTurns    = 2
	exitRateLimit   = 3
	exitPermStuck   = 4
	exitInterrupted = 130
)

var (
	errPermissionStuck = errors.New("permission denied; model could not proceed")
	errInterrupted     = errors.New("interrupted")
)

type permMode string

const (
	permDeny       permMode = "deny"
	permAsk        permMode = "ask"
	permAllowReads permMode = "allow-reads"
)

func parsePermMode(s string) (permMode, error) {
	switch s {
	case "", "deny":
		return permDeny, nil
	case "ask":
		return permAsk, nil
	case "allow-reads":
		return permAllowReads, nil
	default:
		return "", fmt.Errorf("unknown permission-mode %q (want ask|deny|allow-reads)", s)
	}
}

// silentAsker never blocks and never reads a tty. Decision(0)==Deny.
type silentAsker struct{}

func (silentAsker) Ask(context.Context, agent.ToolCall) agent.Decision {
	return agent.Deny
}

// headlessGate wraps the interactive policy. deny and allow-reads convert
// Ask into Deny so the model receives a tool_result and the run continues.
// YOLO is never engaged.
type headlessGate struct {
	inner agent.Gate
	mode  permMode
}

func (g headlessGate) Check(tool string) (agent.Verdict, string) {
	v, why := g.inner.Check(tool)
	if v == agent.VerdictAllow {
		return v, why
	}
	if g.mode == permAsk {
		return v, why
	}
	if v == agent.VerdictAsk {
		return agent.VerdictDeny, "headless " + string(g.mode)
	}
	return v, why
}

func (g headlessGate) Record(tool string, d agent.Decision) {
	g.inner.Record(tool, d)
}

func (g headlessGate) Effective(tool string, d agent.Decision) agent.Decision {
	return g.inner.Effective(tool, d)
}

type noticeStderr struct{ w io.Writer }

func (s noticeStderr) Emit(e agent.Event) error {
	switch e.Type {
	case agent.Notice:
		if e.Text != "" {
			fmt.Fprintln(s.w, e.Text)
		}
	case agent.RunError:
		if e.Err != "" {
			fmt.Fprintln(s.w, e.Err)
		} else if e.Text != "" {
			fmt.Fprintln(s.w, e.Text)
		}
	}
	return nil
}

type jsonlStdout struct{ w io.Writer }

func (s jsonlStdout) Emit(e agent.Event) error {
	b, err := json.Marshal(e.ForStore())
	if err != nil {
		return err
	}
	_, err = s.w.Write(append(b, '\n'))
	return err
}

type headlessConfig struct {
	prompt   string
	json     bool
	maxTurns int
	mode     permMode
	sessDir  string
	stdout   io.Writer
	stderr   io.Writer
	stdin    io.Reader
	provider provider.Provider
}

func finalAssistantText(evs []agent.Event) string {
	evs = agent.Live(evs)
	lastStart := -1
	for i, e := range evs {
		if e.Type == agent.TurnStart {
			lastStart = i
		}
	}
	if lastStart < 0 {
		return ""
	}
	var b strings.Builder
	for _, e := range evs[lastStart:] {
		if e.Type == agent.ToolStart {
			return ""
		}
		if e.Type == agent.TextDelta {
			b.WriteString(e.Text)
		}
		if e.Type == agent.TurnEnd {
			break
		}
	}
	return b.String()
}

func deniedAndStuck(evs []agent.Event, text string) bool {
	if strings.TrimSpace(text) != "" {
		return false
	}
	for _, e := range agent.Live(evs) {
		if e.Type == agent.PermReply && e.Decision == agent.Deny {
			return true
		}
	}
	return false
}

func mapHeadlessExit(err error) int {
	if err == nil {
		return exitSettled
	}
	if errors.Is(err, errInterrupted) {
		return exitInterrupted
	}
	if errors.Is(err, agent.ErrMaxTurns) {
		return exitMaxTurns
	}
	if errors.Is(err, agent.ErrRateLimitBudget) {
		return exitRateLimit
	}
	if errors.Is(err, errPermissionStuck) {
		return exitPermStuck
	}
	return exitError
}

func runHeadless(cfg headlessConfig) int {
	return mapHeadlessExit(runHeadlessErr(cfg))
}

func runHeadlessErr(cfg headlessConfig) error {
	if cfg.stdout == nil {
		cfg.stdout = os.Stdout
	}
	if cfg.stderr == nil {
		cfg.stderr = os.Stderr
	}
	prompt := cfg.prompt
	if prompt == "-" {
		r := cfg.stdin
		if r == nil {
			r = os.Stdin
		}
		b, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		prompt = string(b)
	}
	prompt = strings.TrimRight(prompt, "\n")
	if strings.TrimSpace(prompt) == "" {
		return errors.New("empty prompt")
	}

	prov := cfg.provider
	if prov == nil {
		var err error
		prov, err = pickProvider()
		if err != nil {
			return err
		}
	}

	root, err := tools.NewRoot("")
	if err != nil {
		return err
	}

	journalPath, err := sessionPath(cfg.sessDir)
	if err != nil {
		return err
	}
	journal, err := store.NewJSONL(journalPath)
	if err != nil {
		return err
	}

	sh, err := snap.New(root.Dir())
	if err != nil {
		journal.Close()
		return err
	}
	reg := tools.NewRegistry(root, sh)

	var sinks agent.Fanout
	sinks = append(sinks, journal, noticeStderr{w: cfg.stderr})
	if cfg.json {
		sinks = append(sinks, jsonlStdout{w: cfg.stdout})
	}

	loop := &agent.Loop{
		Provider: prov,
		Tools:    reg,
		Sink:     sinks,
		System:   system,
		Gate:     headlessGate{inner: gate{perm.New(reg)}, mode: cfg.mode},
		Budget:   agent.NewBudget(),
		Human:    silentAsker{},
		MaxTurns: cfg.maxTurns,
	}

	cwd, _ := os.Getwd()
	if err := loop.Start(fmt.Sprintf("%s · %s · %s",
		build.BannerPrefix(), prov.Name(), filepath.Base(cwd)), root.Dir()); err != nil {
		journal.Close()
		return err
	}
	if s := conflictLine(config.Conflicts()); s != "" {
		loop.Note(s)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err = loop.Run(ctx, prompt)
	interrupted := ctx.Err() != nil

	// End the session in the journal first, then close. Surface both errors
	// without masking the original run error.
	endErr := loop.End(fmt.Sprintf(statusSessionEnded, filepath.Base(journalPath)))
	closeErr := journal.Close()
	if closeErr == nil {
		fmt.Fprintln(cfg.stderr, "session:", journalPath)
	}

	if interrupted {
		return errors.Join(errors.New("interrupted"), endErr, closeErr)
	}
	if err != nil {
		return errors.Join(err, endErr, closeErr)
	}
	text := finalAssistantText(loop.Hist())
	if deniedAndStuck(loop.Hist(), text) {
		return errPermissionStuck
	}
	if !cfg.json {
		_, werr := io.WriteString(cfg.stdout, text)
		if werr != nil {
			return errors.Join(werr, endErr, closeErr)
		}
	}
	return errors.Join(endErr, closeErr)
}
