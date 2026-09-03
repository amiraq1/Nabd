// Command ag is nabd: a coding agent that fits in a thumb's reach.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nabd/internal/agent"
	"nabd/internal/perm"
	"nabd/internal/provider"
	"nabd/internal/snap"
	"nabd/internal/store"
	"nabd/internal/tools"
	"nabd/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

const system = `You are nabd, a coding agent working inside a phone terminal 50 columns wide.
Reply in Arabic. Be extremely brief: never repeat the question, never apologise, and never list anything without cause. Two lines suffice when two suffice.`

var (
	version = "dev"
	commit  = "none" // full SHA injected at build time; "none" means a plain `go build`
)

func main() {
	loadEnv()
	replay := flag.String("replay", "", "replay a session.jsonl and exit")
	speed := flag.Float64("speed", 1, "replay multiplier; 0 is instant")
	sessDir := flag.String("dir", "", "session directory (default ~/.ag/sessions)")
	cont := flag.Bool("continue", false, "resume the latest session")
	showVer := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVer {
		fmt.Println(version + " · " + commit)
		return
	}

	if *replay != "" {
		if err := doReplay(*replay, *speed); err != nil {
			die(err)
		}
		return
	}
	if err := doChat(*sessDir, *cont); err != nil {
		die(err)
	}
}

func doReplay(path string, speed float64) error {
	events, err := store.Read(path)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return fmt.Errorf("no events in %s", path)
	}
	_, err = tea.NewProgram(ui.NewReplay(events, speed)).Run()
	return err
}

func doChat(dir string, cont bool) error {
	prov, err := pickProvider()
	if err != nil {
		return err
	}

	// Determine the journal path BEFORE creating the loop: on --continue
	// we open the existing session file itself (append); on a new session
	// we create a fresh file. These two paths must never be confused.
	var journalPath string
	if cont {
		journalPath, err = latestSession(dir)
		if err != nil {
			return err
		}
	} else {
		journalPath, err = sessionPath(dir)
		if err != nil {
			return err
		}
	}

	journal, err := store.NewJSONL(journalPath)
	if err != nil {
		return err
	}
	defer journal.Close()

	// On --continue, seed the loop from the existing events in the journal
	// BEFORE the UI starts emitting, so Seq/Parent continue correctly and
	// every Parent references an event present in this same file.
	var prevEvs []agent.Event
	if cont {
		evs, err := store.Read(journalPath)
		if err != nil {
			return err
		}
		prevEvs = agent.Live(evs)
		fmt.Printf("resumed %s · %d live events of %d\n",
			filepath.Base(journalPath), len(prevEvs), len(evs))
	}

	// The UI is a sink too, behind a buffered channel: a slow terminal
	// must not stall the loop, and the journal must never wait on paint.
	root, err := tools.NewRoot("")
	if err != nil {
		return err
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		return err
	}
	reg := tools.NewRegistry(root, sh)
	pol := perm.New(reg)
	ap := ui.NewApprover()

	ch := make(chan agent.Event, 128)
	loop := &agent.Loop{
		Provider: prov,
		Tools:    reg,
		Sink:     agent.Fanout{journal, chanSink(ch)},
		System:   system,
		Gate:     gate{pol},
		Budget:   agent.NewBudget(),
		Human:    ap,
	}
	if cont {
		loop.Seed(prevEvs)
	}

	cwd, _ := os.Getwd()
	if err := loop.Start(fmt.Sprintf("nabd %s · %s · %s · %s",
		version, commit, prov.Name(), filepath.Base(cwd))); err != nil {
		return err
	}

	chat := ui.NewChat(loop, ch)
	chat.Approve = ap

	chat.OnRewind = func(n int) string {
		txt, err := loop.Rewind(n)
		if err != nil {
			return err.Error()
		}
		chat.SetInput(txt) // the cut turn comes back to the prompt, editable
		s := fmt.Sprintf("rewound %d turns", n)
		if k := len(reg.Pending()); k > 0 {
			s += fmt.Sprintf(" · %d uncommitted edits on disk (/undo %d)", k, k)
		}
		return s
	}

	chat.OnUndo = func(n int) string {
		// The journal is the source of truth, not the in-memory edit log:
		// after a restart (--continue) the edit log is empty, but the
		// edit_record events from the seeded session are still there.
		recs := editRecords(agent.Live(loop.Hist()))
		var b strings.Builder
		for _, r := range reg.PersistedUndo(recs, n) {
			mark := "✗"
			if r.OK {
				mark = "✓"
			}
			if r.Rel == "" {
				fmt.Fprintf(&b, "%s %s\n", mark, r.Note)
				continue
			}
			fmt.Fprintf(&b, "%s %s — %s\n", mark, r.Rel, r.Note)
		}
		s := strings.TrimRight(b.String(), "\n")
		// The journal is the single source of truth: emit the undo as a
		// Notice so the event survives in session.jsonl and reaches the UI
		// through the event channel. Returning "" keeps the status line from
		// duplicating what the Notice renders.
		loop.Note(fmt.Sprintf("/undo %d — %s", n, s))
		return ""
	}

	chat.OnEdits = func() string {
		p := reg.Pending()
		if len(p) == 0 {
			return "no reversible edits pending"
		}
		var b strings.Builder
		for i, e := range p {
			fmt.Fprintf(&b, "%d· %s %s\n", i+1, e.Tool, e.Rel)
		}
		return strings.TrimRight(b.String(), "\n")
	}

	chat.OnCtx = func() string {
		ms := agent.Squeeze(agent.Messages(agent.Live(loop.Hist())), agent.KeepFullRounds)
		p := loop.Budget.Pressure(ms)
		return fmt.Sprintf("context %d%% (%d / %d tokens)", int(p*100), loop.Budget.Estimate(ms), loop.Budget.Usable())
	}
	chat.OnCompact = func() string {
		// Return immediately so the UI stays responsive; run compaction in a
		// background goroutine and surface the result via the event channel.
		go func() {
			if err := loop.Compact(context.Background(), loop.Budget.Usable()*4/10); err != nil {
				loop.Note("compact failed: " + err.Error())
			}
		}()
		return statusCompacting
	}

	_, err = tea.NewProgram(chat).Run()
	if err != nil {
		return err
	}
	// Mark the session as finished before closing the journal so the
	// terminal state is durably recorded. End() emits exactly one RunEnd.
	_ = loop.End(fmt.Sprintf(statusSessionEnded, filepath.Base(journalPath)))
	fmt.Println("session:", journalPath)
	return nil
}

// chanSink hands events to the UI, dropping nothing but never blocking
// forever: if the UI is gone, the journal still gets everything.
type chanSink chan agent.Event

func (c chanSink) Emit(e agent.Event) error {
	select {
	case c <- e:
	case <-time.After(2 * time.Second):
	}
	return nil
}

// sessionPath returns a fresh, unique path for a new session. The name uses
// millisecond precision to avoid collisions when two sessions start in the
// same second. Callers must not call this when --continue is set.
func sessionPath(dir string) (string, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".ag", "sessions")
	}
	name := time.Now().UTC().Format("20060102-150405.000") + ".jsonl"
	return filepath.Join(dir, name), nil
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "ag:", err)
	os.Exit(1)
}

// loadEnv reads ~/.ag/env and populates missing environment variables.
func loadEnv() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	b, err := os.ReadFile(filepath.Join(home, ".ag", "env"))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if idx := strings.IndexByte(line, '='); idx > 0 {
			k := strings.TrimSpace(line[:idx])
			v := strings.TrimSpace(line[idx+1:])
			v = strings.Trim(v, `"'`)
			if os.Getenv(k) == "" {
				os.Setenv(k, v)
			}
		}
	}
}

// pickProvider prefers whatever key is present. NABD_PROVIDER forces one.
func pickProvider() (provider.Provider, error) {
	switch os.Getenv("NABD_PROVIDER") {
	case "nvidia":
		return provider.NewNVIDIA()
	case "anthropic":
		return provider.NewAnthropic()
	case "openrouter":
		return provider.NewOpenRouter()
	case "groq":
		return provider.NewGroq(), nil
	}
	if os.Getenv("GROQ_API_KEY") != "" {
		return provider.NewGroq(), nil
	}
	if os.Getenv("OPENROUTER_API_KEY") != "" {
		return provider.NewOpenRouter()
	}
	if os.Getenv("NVIDIA_API_KEY") != "" {
		return provider.NewNVIDIA()
	}
	return provider.NewAnthropic()
}

// gate translates the policy's vocabulary into the loop's. It is the only
// place the two packages meet, and it fails closed by default.
type gate struct{ p *perm.Policy }

func (g gate) Check(tool string) (agent.Verdict, string) {
	v, why := g.p.Check(tool)
	switch v {
	case perm.Allow:
		return agent.VerdictAllow, why
	case perm.Deny:
		return agent.VerdictDeny, why
	}
	return agent.VerdictAsk, why
}

func (g gate) Record(tool string, d agent.Decision) {
	if d == agent.AllowSession {
		g.p.Record(tool, agent.AllowSession)
	}
}

func (g gate) Effective(tool string, d agent.Decision) agent.Decision {
	return g.p.Effective(tool, d)
}

// latestSession returns the path to the most recent *.jsonl in the session
// directory. It respects an explicit --dir; otherwise it defaults to
// ~/.ag/sessions. Returns a clear error when no session exists.
func latestSession(dir string) (string, error) {
	sessDir := dir
	if sessDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		sessDir = filepath.Join(home, ".ag", "sessions")
	}
	ents, err := os.ReadDir(sessDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf(errNoSessions, sessDir)
		}
		return "", err
	}
	var last string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			name := e.Name()
			if name > last {
				last = name
			}
		}
	}
	if last == "" {
		return "", fmt.Errorf(errNoSessions, sessDir)
	}
	return filepath.Join(sessDir, last), nil
}

// editRecords pulls the persisted edit fingerprints out of a live branch,
// newest first. This is what lets /undo work after a restart: the records
// survive in the journal even though the in-memory edit log is gone.
func editRecords(evs []agent.Event) []*agent.EditRecord {
	var out []*agent.EditRecord
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Type == agent.EventEdit && evs[i].Edit != nil {
			out = append(out, evs[i].Edit)
		}
	}
	return out
}
