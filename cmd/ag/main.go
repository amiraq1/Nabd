// Command ag is nabd: a coding agent that fits in a thumb's reach.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nabd/internal/agent"
	"nabd/internal/config"
	"nabd/internal/perm"
	"nabd/internal/provider"
	"nabd/internal/snap"
	"nabd/internal/store"
	"nabd/internal/tools"
	"nabd/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

// Feed UI tuning constants.
const (
	// eventBatchInterval is how long the batcher waits before flushing a
	// partial batch of events to the UI.
	eventBatchInterval = 20 * time.Millisecond
	// maxEventBatchSize forces a flush when this many events accumulate
	// within one interval.
	maxEventBatchSize = 128
)

const system = `You are nabd, a coding agent working inside a phone terminal 50 columns wide.
Reply in Arabic. Be extremely brief: never repeat the question, never apologise, and never list anything without cause. Two lines suffice when two suffice.`

var (
	version = "dev"
	commit  = "none" // full SHA injected at build time; "none" means a plain `go build`
)

func main() {
	// NOTE: no legacy ~/.ag/env loading here. Environment-isolation policy
	// (Phase 3) removed it: the only config source is ~/.ag/config, parsed by
	// config.Load() into a private map — never merged into os.Environ, so no
	// provider key ever reaches the process-global environment or any child
	// process spawned by the bash tool.
	replay := flag.String("replay", "", "replay a session.jsonl and exit")
	speed := flag.Float64("speed", 1, "replay multiplier; 0 is instant")
	sessDir := flag.String("dir", "", "session directory (default ~/.ag/sessions)")
	cont := flag.Bool("continue", false, "resume the latest session")
	showVer := flag.Bool("version", false, "print version and exit")
	useFeed := flag.Bool("feed", false, "use the new projected feed UI (experimental)")
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
	if *useFeed {
		if err := doChatWithFeed(*sessDir, *cont); err != nil {
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
	root, err := tools.NewRoot("")
	if err != nil {
		return err
	}

	var journalPath string
	if cont {
		journalPath, err = latestSession(dir, root.Dir())
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
		version, commit, prov.Name(), filepath.Base(cwd)), root.Dir()); err != nil {
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
		return fmt.Sprintf("rewound %d turns · disk edits remain, /undo does not cover edits after branch cut", n)
	}

	chat.OnUndo = func(n int) string { return fileUndo(loop, reg, n) }

	chat.OnEdits = func() string {
		p := editRecords(agent.Live(loop.Hist()))
		if len(p) == 0 {
			return "no reversible edits pending"
		}
		var b strings.Builder
		for i, e := range p {
			tool := "edit_file"
			if e.Patch == "" {
				tool = "write_file"
			}
			fmt.Fprintf(&b, "%d· %s %s\n", i+1, tool, e.Path)
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

// doChatWithFeed is the Phase 3A UI path: agent events flow through the
// presentation Projector into a scrollable viewport, and user input flows
// through a multiline composer and the deterministic input router. It is
// opt-in via the -feed flag while the default Chat UI remains the stable
// fallback.
func doChatWithFeed(dir string, cont bool) error {
	// Install the Arabic limit notice for the composer (internal/ui keeps
	// ASCII string literals; user-facing Arabic lives here).
	ui.SetLimitNotice(limitNoticeArabic)

	prov, err := pickProvider()
	if err != nil {
		return err
	}

	root, err := tools.NewRoot("")
	if err != nil {
		return err
	}

	var journalPath string
	if cont {
		journalPath, err = latestSession(dir, root.Dir())
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

	sh, err := snap.New(root.Dir())
	if err != nil {
		return err
	}
	reg := tools.NewRegistry(root, sh)
	pol := perm.New(reg)
	ap := ui.NewApprover()

	// Create the feed model and the event batcher.
	feed := ui.NewFeed()

	// Create the loop BEFORE wiring callbacks (callbacks reference it).
	loop := &agent.Loop{
		Provider: prov,
		Tools:    reg,
		System:   system,
		Gate:     gate{pol},
		Budget:   agent.NewBudget(),
		Human:    ap,
	}
	if cont {
		loop.Seed(prevEvs)
	}

	// Sink: journal first (durable), then batcher → feed. The batcher's
	// flush callback delivers batches as Bubble Tea messages (prog.Send),
	// so no goroutine ever mutates the feed model off the event loop.
	batcher := ui.NewBatcher(eventBatchInterval, maxEventBatchSize, func(batch []agent.Event) {
		feed.SendBatch(batch)
	})
	batcher.Start()
	defer batcher.Stop()

	loop.Sink = agent.Fanout{journal, feedSink{batcher: batcher}}

	// The feed starts each run directly on the loop (same contract as the
	// classic Chat path: one Run per accepted message, events come back
	// through the sink). It answers permission asks through the same
	// approver the loop blocks on.
	feed.SetRunner(loop)
	feed.SetApprover(ap)

	// Wire up command callbacks. OnRewind restores the cut turn into the
	// composer for editing (same contract as the classic Chat path).
	feed.SetCallbacks(&ui.FeedCallbacks{
		OnUndo:    func(n int) string { return fileUndo(loop, reg, n) },
		OnCompact: func() string { return chatOnCompact(loop) },
		OnRewind: func(n int) (string, string) {
			if loop == nil {
				return "", "rewind not supported"
			}
			txt, err := loop.Rewind(n)
			if err != nil {
				return "", err.Error()
			}
			return txt, fmt.Sprintf("rewound %d turns · disk edits remain, /undo does not cover edits after branch cut", n)
		},
		OnCtx: func() string {
			ms := agent.Squeeze(agent.Messages(agent.Live(loop.Hist())), agent.KeepFullRounds)
			p := loop.Budget.Pressure(ms)
			return fmt.Sprintf("context %d%% (%d / %d tokens)", int(p*100), loop.Budget.Estimate(ms), loop.Budget.Usable())
		},
		OnEdits: func() string {
			p := editRecords(agent.Live(loop.Hist()))
			if len(p) == 0 {
				return "no reversible edits pending"
			}
			var b strings.Builder
			for i, e := range p {
				tool := "edit_file"
				if e.Patch == "" {
					tool = "write_file"
				}
				fmt.Fprintf(&b, "%d· %s %s\n", i+1, tool, e.Path)
			}
			return strings.TrimRight(b.String(), "\n")
		},
	})

	// Initialize the feed from seeded events (replay).
	if len(prevEvs) > 0 {
		feed.BuildFromEvents(agent.Live(prevEvs))
	}

	// Start the program first: the batcher delivers event batches via
	// prog.Send, which BLOCKS until the program's event loop is running.
	// loop.Start below emits the RunStart banner through the batcher, so
	// the program must already be consuming messages.
	prog := tea.NewProgram(feed, feed.ProgramOptions()...)
	feed.SetProgram(prog)

	progDone := make(chan error, 1)
	go func() {
		_, err := prog.Run()
		progDone <- err
	}()

	// Give the program's event loop a moment to start consuming before the
	// first event arrives. prog.Send blocks until the loop is ready, so the
	// RunStart below will simply wait; no event is lost.
	if err := loop.Start(fmt.Sprintf("nabd %s · %s · %s · %s",
		version, commit, prov.Name(), filepath.Base(journalPath)), root.Dir()); err != nil {
		return err
	}

	if err := <-progDone; err != nil {
		return err
	}
	_ = loop.End(fmt.Sprintf(statusSessionEnded, filepath.Base(journalPath)))
	fmt.Println("session:", journalPath)
	return nil
}

// feedSink adapts the batcher to agent.Sink.
type feedSink struct {
	batcher *ui.Batcher
}

func (s feedSink) Emit(e agent.Event) error {
	s.batcher.Add(e)
	return nil
}

// fileUndo rewinds file edits recorded in the journal (not conversation
// turns — that is /rewind). It is the single implementation shared by the
// classic Chat UI and the feed UI so both surfaces behave identically:
//
//   - the journal is the source of truth, not the in-memory edit log: after
//     a restart (--continue) the edit log is empty, but the edit_record
//     events from the seeded session are still there;
//   - the undo is emitted as exactly one Notice so it survives in
//     session.jsonl and reaches the UI through the event channel;
//   - "" is returned so the UI status line does not duplicate what the
//     Notice renders. A shortfall (no records) returns a visible message
//     because the Notice text would be empty.
func fileUndo(loop *agent.Loop, reg *tools.Registry, n int) string {
	if loop == nil || reg == nil {
		return "undo not supported"
	}
	recs := editRecords(agent.Live(loop.Hist()))
	if len(recs) == 0 {
		return "no edits to undo"
	}
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
	loop.Note(fmt.Sprintf("/undo %d — %s", n, s))
	return ""
}

func chatOnCompact(loop *agent.Loop) string {
	if loop == nil {
		return "compact not supported"
	}
	go func() {
		if err := loop.Compact(context.Background(), loop.Budget.Usable()*4/10); err != nil {
			loop.Note("compact failed: " + err.Error())
		}
	}()
	return statusCompacting
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

// pickProvider prefers whatever key is present, in ~/.ag/config first and
// the environment second. NABD_PROVIDER forces one. A config file with loose
// permissions is a hard error here rather than a silent fallback: the user
// wrote a key down and believes it is protected.
func pickProvider() (provider.Provider, error) {
	if err := config.Load(); err != nil {
		return nil, err
	}
	switch config.Get("NABD_PROVIDER") {
	case "router":
		return pickRouterProvider()
	case "nvidia":
		return provider.NewNVIDIA()
	case "anthropic":
		return provider.NewAnthropic()
	case "openrouter":
		return provider.NewOpenRouter()
	case "groq":
		return provider.NewGroq()
	case "":
		switch {
		case config.Has("GROQ_API_KEY"):
			return provider.NewGroq()
		case config.Has("OPENROUTER_API_KEY"):
			return provider.NewOpenRouter()
		case config.Has("NVIDIA_API_KEY"):
			return provider.NewNVIDIA()
		case config.Has("ANTHROPIC_API_KEY"):
			return provider.NewAnthropic()
		}
		return nil, errMissingKey
	}
	return nil, fmt.Errorf("unknown NABD_PROVIDER %q · values: nvidia, anthropic, openrouter, groq, router",
		config.Get("NABD_PROVIDER"))
}

// errMissingKey is the first-run wall: it names every supported key so a
// phone user is not sent hunting through the source to find the one that
// works.
var errMissingKey = errors.New(`no provider key found in ~/.ag/config or the environment.
Set one key and restart, or force the provider:
  ANTHROPIC_API_KEY=...   (anthropic)
  NVIDIA_API_KEY=...      (nvidia)
  OPENROUTER_API_KEY=...  (openrouter)
  GROQ_API_KEY=...        (groq)
  NABD_PROVIDER=nvidia|anthropic|openrouter|groq|router`)

func pickRouterProvider() (provider.Provider, error) {
	if config.Has("NABD_BASE_URL") {
		return nil, errors.New("NABD_BASE_URL is not allowed when NABD_PROVIDER=router (base URLs are determined per-route)")
	}
	if config.Has("NABD_MODEL") {
		fmt.Fprintf(os.Stderr, "notice: NABD_MODEL is ignored when NABD_PROVIDER=router; models are determined by NABD_ROUTES\n")
	}
	_, err := provider.ParseRouterMode(config.Get("NABD_ROUTER_MODE"))
	if err != nil {
		return nil, err
	}

	routesRaw := config.Get("NABD_ROUTES")
	if routesRaw == "" {
		return nil, errors.New("NABD_ROUTES is required when NABD_PROVIDER=router")
	}
	entries, err := provider.ParseRoutes(routesRaw)
	if err != nil {
		return nil, err
	}

	if err := provider.ValidateRouteKeys(entries); err != nil {
		return nil, err
	}

	timeoutSec, err := provider.ParsePrestreamTimeout(config.Get("NABD_ROUTER_PRESTREAM_TIMEOUT"))
	if err != nil {
		return nil, err
	}

	var routes []provider.Route
	for _, entry := range entries {
		r, err := provider.BuildRoute(entry)
		if err != nil {
			return nil, err
		}
		routes = append(routes, r)
	}

	return provider.NewRouter(routes, time.Duration(timeoutSec)*time.Second, provider.RealClock{})
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
func latestSession(dir, projectRoot string) (string, error) {
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

	// ReadDir returns sorted by name (timestamp). We iterate backwards to find the newest.
	for i := len(ents) - 1; i >= 0; i-- {
		e := ents[i]
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			name := e.Name()
			path := filepath.Join(sessDir, name)

			// Check if project root matches
			events, err := store.Read(path)
			if err != nil {
				continue
			}

			for _, ev := range events {
				if ev.Type == agent.RunStart {
					if ev.ProjectRoot == "" {
						// Legacy session without root metadata is skipped
						continue
					}
					if ev.ProjectRoot == projectRoot {
						return path, nil
					}
					break
				}
			}
		}
	}
	return "", fmt.Errorf(errNoSessions, sessDir)
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
