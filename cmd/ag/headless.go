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
	"nabd/internal/payload"
	"nabd/internal/perm"
	"nabd/internal/provider"
	"nabd/internal/skill"
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

// silentAsker never blocks and never reads a tty. Decision(0)==Deny.
type silentAsker struct{}

func (silentAsker) Ask(context.Context, agent.ToolCall) agent.Decision {
	return agent.Deny
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

type jsonlStdout struct {
	w      io.Writer
	redact store.EventRedactor
}

func (s jsonlStdout) Emit(e agent.Event) error {
	output := e
	if s.redact != nil {
		output = s.redact(e)
	}

	b, err := json.Marshal(output.ForStore())
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
	mode     perm.Mode
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
	err := runHeadlessErr(cfg)
	if err != nil && mapHeadlessExit(err) == exitError {
		w := cfg.stderr
		if w == nil {
			w = os.Stderr
		}
		fmt.Fprintf(w, "nabd: %v\n", err)
	}
	return mapHeadlessExit(err)
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

	if err := config.Load(); err != nil {
		return err
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

	journal, journalPath, err := newSessionJournalWithWarning(cfg.sessDir, cfg.stderr)
	if err != nil {
		return err
	}

	sh, err := snap.New(root.Dir())
	if err != nil {
		journal.Close()
		return err
	}
	reg := tools.NewRegistry(root, sh)
	allSkills, diagnostics, err := loadSessionSkills(root)
	if err != nil {
		journal.Close()
		return err
	}
	for _, d := range diagnostics {
		fmt.Fprintln(cfg.stderr, d.String())
	}
	reg.SetSkillIndex(func() []skill.Skill { return allSkills })
	pol := perm.New(reg)
	wirePathRule(root, reg, pol)
	pol.SetMode(cfg.mode)

	var sinks agent.Fanout
	sinks = append(sinks, journal, noticeStderr{w: cfg.stderr})
	if cfg.json {
		sinks = append(sinks, jsonlStdout{
			w:      cfg.stdout,
			redact: journalEventRedactor(),
		})
	}

	loop := newSessionLoop(prov, reg, gate{pol}, silentAsker{})
	promptSkills, promptDiag := skill.FormatForPrompt(allSkills)
	for _, d := range promptDiag {
		fmt.Fprintln(cfg.stderr, d.String())
	}
	loop.Prompter = &agent.Prompter{Base: payload.DefaultSystemPrompt}
	loop.PromptSections = func() []agent.Section {
		sections := reg.PromptSections()
		if promptSkills != "" {
			sections = append(sections, agent.Section{Name: "skills", Body: promptSkills})
		}
		return sections
	}
	loop.SkillInventory = skill.JournalRecords(allSkills)
	loop.Sink = sinks
	loop.MaxTurns = cfg.maxTurns

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
	reportSession(cfg.stderr, cfg.stderr, journalPath, closeErr)

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
