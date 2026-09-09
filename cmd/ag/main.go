// Command ag is nabd: a coding agent that fits in a thumb's reach.
// The installable binary is named nabd; the package path stays ./cmd/ag
// because ag collides with the_silver_searcher on a typical PATH.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"nabd/internal/agent"
	"nabd/internal/build"
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
	feedTouch := flag.Bool("feed-touch", false, "enable finger-swipe touch scrolling for feed UI")
	prompt := flag.String("p", "", "headless one-shot task; \"-\" reads stdin")
	jsonOut := flag.Bool("json", false, "headless: emit journal JSONL on stdout")
	maxTurns := flag.Int("max-turns", 0, "override turn ceiling")
	permModeFlag := flag.String("permission-mode", "deny", "headless: ask|deny|allow-reads")
	flag.Parse()

	if *showVer {
		fmt.Println(build.Line())
		return
	}

	if *prompt != "" {
		mode, err := parsePermMode(*permModeFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, "nabd:", err)
			os.Exit(exitError)
		}
		os.Exit(runHeadless(headlessConfig{
			prompt:   *prompt,
			json:     *jsonOut,
			maxTurns: *maxTurns,
			mode:     mode,
			sessDir:  *sessDir,
		}))
	}

	if *replay != "" {
		if err := doReplay(*replay, *speed); err != nil {
			die(err)
		}
		return
	}
	if *useFeed {
		if err := doChatWithFeed(*sessDir, *cont, *feedTouch); err != nil {
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

	var prevEvs []agent.Event
	if cont {
		evs, err := store.Read(journalPath)
		if err != nil {
			journal.Close()
			return err
		}
		prevEvs = agent.Live(evs)
		fmt.Printf("resumed %s · %d live events of %d\n",
			filepath.Base(journalPath), len(prevEvs), len(evs))
	}

	sh, err := snap.New(root.Dir())
	if err != nil {
		journal.Close()
		return err
	}
	reg := tools.NewRegistry(root, sh)
	pol := perm.New(reg)
	ap := ui.NewApprover()

	ch := make(chan agent.Event, 128)
	uiSink := &chanSink{ch: ch}
	loop := &agent.Loop{
		Provider: prov,
		Tools:    reg,
		Sink:     agent.Fanout{journal, uiSink},
		System:   system,
		Gate:     gate{pol},
		Budget:   agent.NewBudget(),
		Human:    ap,
	}
	if cont {
		loop.Seed(prevEvs)
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

	chat := ui.NewChat(loop, ch)
	chat.Approve = ap

	chat.OnRewind = func(n int) string {
		txt, err := loop.Rewind(n)
		if err != nil {
			return err.Error()
		}
		chat.SetInput(txt)
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
		go func() {
			if err := loop.Compact(context.Background(), loop.Budget.Usable()*4/10); err != nil {
				loop.Note("compact failed: " + err.Error())
			}
		}()
		return statusCompacting
	}

	_, err = tea.NewProgram(chat).Run()
	if err != nil {
		journal.Close()
		return err
	}

	// Shutdown order: surface any UI drops as a journal Notice, then mark the
	// session ended in the journal (durability), then close the journal.
	// Either step can fail independently; surface both without masking the
	// original. The "session:" line is only printed when the durable close
	// succeeds.
	uiSink.noteDrops(loop)
	endErr := loop.End(fmt.Sprintf(statusSessionEnded, filepath.Base(journalPath)))
	closeErr := journal.Close()
	if closeErr == nil {
		fmt.Println("session:", journalPath)
	}
	return errors.Join(endErr, closeErr)
}

func doChatWithFeed(dir string, cont bool, feedTouch bool) error {
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

	var prevEvs []agent.Event
	if cont {
		evs, err := store.Read(journalPath)
		if err != nil {
			journal.Close()
			return err
		}
		prevEvs = agent.Live(evs)
		fmt.Printf("resumed %s · %d live events of %d\n",
			filepath.Base(journalPath), len(prevEvs), len(evs))
	}

	sh, err := snap.New(root.Dir())
	if err != nil {
		journal.Close()
		return err
	}
	reg := tools.NewRegistry(root, sh)
	pol := perm.New(reg)
	ap := ui.NewApprover()

	feed := ui.NewFeed()
	feed.SetTouch(feedTouch)

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

	batcher := ui.NewBatcher(eventBatchInterval, maxEventBatchSize, func(batch []agent.Event) {
		feed.SendBatch(batch)
	})
	batcher.Start()

	loop.Sink = agent.Fanout{journal, feedSink{batcher: batcher}}

	feed.SetRunner(loop)
	feed.SetApprover(ap)

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

	if len(prevEvs) > 0 {
		feed.BuildFromEvents(agent.Live(prevEvs))
	}

	prog := tea.NewProgram(feed, feed.ProgramOptions()...)
	feed.SetProgram(prog)

	progDone := make(chan error, 1)
	go func() {
		_, err := prog.Run()
		progDone <- err
	}()

	if err := loop.Start(fmt.Sprintf("%s · %s · %s",
		build.BannerPrefix(), prov.Name(), filepath.Base(journalPath)), root.Dir()); err != nil {
		batcher.Stop()
		journal.Close()
		return err
	}

	if s := conflictLine(config.Conflicts()); s != "" {
		loop.Note(s)
	}

	// Stop the batcher before shutdown so no new events race the End marker.
	batcher.Stop()

	if err := <-progDone; err != nil {
		journal.Close()
		return err
	}

	endErr := loop.End(fmt.Sprintf(statusSessionEnded, filepath.Base(journalPath)))
	closeErr := journal.Close()
	if closeErr == nil {
		fmt.Println("session:", journalPath)
	}
	return errors.Join(endErr, closeErr)
}

type feedSink struct {
	batcher *ui.Batcher
}

func (s feedSink) Emit(e agent.Event) error {
	s.batcher.Add(e)
	return nil
}

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
		mark := "x"
		if r.OK {
			mark = "ok"
		}
		if r.Rel == "" {
			fmt.Fprintf(&b, "%s %s\n", mark, r.Note)
			continue
		}
		fmt.Fprintf(&b, "%s %s - %s\n", mark, r.Rel, r.Note)
	}
	s := strings.TrimRight(b.String(), "\n")
	loop.Note(fmt.Sprintf("/undo %d - %s", n, s))
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

// chanSink delivers the live event stream to the interactive UI. The UI
// channel is a best-effort view — the journal is the durable source of
// truth — so a full channel must never stall the agent loop or kill the
// session. Emit drops the event instantly, counts the loss, and always
// returns nil, which keeps a UI hiccup from propagating through Fanout as
// a fatal loop error. The drop count is surfaced as a journal Notice just
// before RunEnd (see noteDrops).
type chanSink struct {
	ch      chan agent.Event
	dropped atomic.Int64
}

func (s *chanSink) Emit(e agent.Event) error {
	select {
	case s.ch <- e:
	default:
		s.dropped.Add(1)
	}
	return nil
}

// Dropped reports how many events never reached the UI.
func (s *chanSink) Dropped() int64 { return s.dropped.Load() }

// noteDrops records the dropped-event count as a Notice, once, just before
// the session's RunEnd event. It must be called from the session-end path,
// never from inside Emit: loop.emit holds l.mu while sinks run, so calling
// back into the loop from a sink would deadlock (see NOTES.md P0-1.5).
func (s *chanSink) noteDrops(loop *agent.Loop) {
	if n := s.Dropped(); n > 0 {
		loop.Note(fmt.Sprintf("ui dropped %d event(s) · journal has the full record", n))
	}
}

func sessionPath(dir string) (string, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".ag", "sessions")
		// nabd owns this directory: tighten it to 0o700 so that no other
		// uid can list or open session journals.  We do this only for the
		// default path — a caller-supplied --dir is their responsibility.
		if err := ensureDefaultSessionDir(dir); err != nil {
			return "", err
		}
	}
	name := time.Now().UTC().Format("20060102-150405.000") + ".jsonl"
	return filepath.Join(dir, name), nil
}

// ensureDefaultSessionDir creates dir with mode 0o700 if it does not exist, or
// tightens an existing directory to 0o700 if it is wider.  It only touches
// directories under ~/.ag that nabd creates and owns; it never modifies a
// caller-supplied --dir path.
func ensureDefaultSessionDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// MkdirAll does not tighten an existing directory, so chmod explicitly.
	// This migrates a legacy 0o755 directory to 0o700 on first run.
	return os.Chmod(dir, 0o700)
}

func conflictLine(cs []config.Conflict) string {
	if len(cs) == 0 {
		return ""
	}
	names := make([]string, 0, len(cs))
	for _, c := range cs {
		clean := sanitizeKey(c.Key)
		if clean != "" {
			names = append(names, clean)
		}
	}
	if len(names) == 0 {
		return ""
	}
	slices.Sort(names)
	names = slices.Compact(names)
	return fmt.Sprintf(conflictNotice, strings.Join(names, conflictSep))
}

func sanitizeKey(k string) string {
	k = strings.ReplaceAll(k, "\r", "")
	k = strings.ReplaceAll(k, "\n", "")
	clean := ui.SanitizeForDisplay(k, ui.DisplayPolicy{AllowNewline: false})
	return strings.TrimSpace(clean)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "nabd:", err)
	os.Exit(1)
}

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
	}

	if config.Has("GROQ_API_KEY") {
		return provider.NewGroq()
	}
	if config.Has("OPENROUTER_API_KEY") {
		return provider.NewOpenRouter()
	}
	if config.Has("NVIDIA_API_KEY") {
		return provider.NewNVIDIA()
	}
	return provider.NewAnthropic()
}

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

func latestSession(dir, projectRoot string) (string, error) {
	sessDir := dir
	if sessDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		sessDir = filepath.Join(home, ".ag", "sessions")
		// Same ownership rule as sessionPath: nabd owns the default
		// directory, so tighten it to 0o700 before reading it. A legacy
		// world-readable dir must not stay readable just because the user
		// resumed a session instead of starting a fresh one.
		if err := ensureDefaultSessionDir(sessDir); err != nil {
			return "", err
		}
	}
	ents, err := os.ReadDir(sessDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf(errNoSessions, sessDir)
		}
		return "", err
	}

	for i := len(ents) - 1; i >= 0; i-- {
		e := ents[i]
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			path := filepath.Join(sessDir, e.Name())
			events, err := store.Read(path)
			if err != nil {
				continue
			}
			for _, ev := range events {
				if ev.Type == agent.RunStart {
					if ev.ProjectRoot == "" {
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

func editRecords(evs []agent.Event) []*agent.EditRecord {
	var out []*agent.EditRecord
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Type == agent.EventEdit && evs[i].Edit != nil {
			out = append(out, evs[i].Edit)
		}
	}
	return out
}
