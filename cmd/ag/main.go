// Command ag is nabd: a coding agent that fits in a thumb's reach.
// The installable binary is named nabd; the package path stays ./cmd/ag
// because ag collides with the_silver_searcher on a typical PATH.
package main

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"nabd/internal/agent"
	"nabd/internal/build"
	"nabd/internal/config"
	"nabd/internal/payload"
	"nabd/internal/perm"
	"nabd/internal/provider"
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

// The interactive UI surfaces named by --ui and NABD_UI (ADR-0001). Chat was
// retired when the v1.6.0 and v1.7.0 stages were collapsed, so feed is the
// only surface that can run; uiChat is still named here so a request for it
// gets a migration message rather than an unknown-value error.
const (
	uiFeed = "feed"
	uiChat = "chat"
)

// exitUsage is the shell's conventional code for a rejected command line.
// ADR-0001's flag rules reject with it, so a mistyped flag stays
// distinguishable from a failed run (exitError).
const exitUsage = 2

// newSessionLoop builds the Loop the two entry points share: the same
// model-facing system prompt, the same permission gate, the same context
// budget. Callers set only what differs — the provider, the sinks, and any
// turn ceiling.
//
// This exists because the prompt is a security-relevant contract, not a
// string: two literals that happen to agree today are two places to diverge
// tomorrow. TestSessionLoopPromptHasNoDivergentPaths pins it, and the prompt
// itself lives in internal/payload because its size is a budgeted cost term
// (see NBD-403).
//
// The entry points are Feed (interactive) and headless (-p). The third
// interactive surface, Chat, was retired by ADR-0001; replay is a read-only
// projector over an existing journal and builds no session.
func newSessionLoop(prov provider.Provider, reg *tools.Registry, g agent.Gate, human agent.Asker) *agent.Loop {
	// The read ceiling follows the provider's own declaration (NBD-404). This
	// constructor is the single point both entry points pass through, so the
	// cap cannot differ between Feed and headless. An explicit NABD_MAX_READ
	// still wins — SetReadCap decides that, not this call.
	if prov != nil {
		if rc, ok := prov.(provider.ReadCapper); ok {
			tools.SetReadCap(rc.ReadCapBytes())
		}
	}
	loop := &agent.Loop{
		Provider:    prov,
		Tools:       reg,
		System:      payload.DefaultSystemPrompt,
		Gate:        g,
		Budget:      agent.NewBudget(),
		SpendBudget: agent.NewSpendBudget(),
		Human:       human,
	}
	// A repaired tool call is announced in the journal before it runs: a repair
	// the user never sees is one that did not happen. Wiring it here rather
	// than at each entry point is what keeps Feed and headless identical
	// (see TestSessionLoopPromptHasNoDivergentPaths).
	if reg != nil {
		reg.OnRepair = func(f tools.Fix) { loop.Note(f.Notice()) }
		reg.OnMutationPrepared = loop.PrepareMutation
		reg.OnMutationAborted = loop.AbortMutation
	}
	return loop
}

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
	uiFlag := flag.String("ui", "", "interactive UI: feed (the only surface; the chat UI was retired per ADR-0001)")
	useFeed := flag.Bool("feed", true, "deprecated: use --ui=feed")
	feedTouch := flag.Bool("feed-touch", false, "enable finger-swipe touch scrolling for feed UI")
	prompt := flag.String("p", "", "headless one-shot task; \"-\" reads stdin")
	jsonOut := flag.Bool("json", false, "headless: emit journal JSONL on stdout")
	maxTurns := flag.Int("max-turns", 0, "override turn ceiling")
	permModeFlag := flag.String("permission-mode", "", "ask|deny|allow-reads|plan (interactive and headless; empty = path default)")
	exportPath := flag.String("export", "", "export a session journal as JSONL to stdout and exit")
	exportRedact := flag.Bool("redact", false, "with --export: redact recognized credential patterns")
	flag.Parse()

	provided := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { provided[f.Name] = true })
	// ADR-0001: --ui and --feed select the interactive surface, so they have no
	// meaning beside a non-interactive mode. Reject the combination instead of
	// accepting a flag that then does nothing.
	rejectUIFlagWith(*replay, *prompt, *exportPath, provided)
	if err := checkExportFlags(*exportPath, *exportRedact, flag.NArg(), provided); err != nil {
		die(err)
	}
	if *exportPath != "" {
		if err := exportJournal(*exportPath, *exportRedact, os.Stdout, os.Stderr); err != nil {
			die(err)
		}
		return
	}

	if *showVer {
		fmt.Println(build.Line())
		return
	}

	// Resolve the permission mode once. An empty flag means "use each path's
	// default": interactive defaults to ask (the current behaviour), headless
	// defaults to deny. An explicit value applies to both, so a single flag
	// cannot silently change interactive behaviour.
	mode, err := perm.ParseMode(*permModeFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nabd:", err)
		os.Exit(exitError)
	}
	interactiveMode, headlessMode := mode, mode
	if *permModeFlag == "" {
		interactiveMode = perm.ModeAsk
		headlessMode = perm.ModeDeny
	}

	if *prompt != "" {
		os.Exit(runHeadless(headlessConfig{
			prompt:   *prompt,
			json:     *jsonOut,
			maxTurns: *maxTurns,
			mode:     headlessMode,
			sessDir:  *sessDir,
		}))
	}

	if *replay != "" {
		if err := doReplay(*replay, *speed); err != nil {
			die(err)
		}
		return
	}
	// Resolve the interactive surface before any UI work: ADR-0001 makes the
	// flag rules explicit, and a conflict must be refused rather than settled
	// by accident. Chat was retired, so feed is the surface that runs.
	surface, err := checkUISurface(*uiFlag, *useFeed, provided)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nabd:", err)
		os.Exit(exitUsage)
	}
	if surface != uiFeed {
		// Unreachable while feed is the only surface. It stays so that adding
		// a surface later cannot silently run the wrong one.
		fmt.Fprintf(os.Stderr, "nabd: unknown interactive surface %q\n", surface)
		os.Exit(exitUsage)
	}
	if provided["feed-touch"] && !touchAllowed(os.Getenv("TERMUX_VERSION"), os.Getenv("NABD_FORCE_TOUCH")) {
		die(fmt.Errorf("-feed-touch captures touch events as mouse input on Termux, " +
			"which prevents the on-screen keyboard from opening. Keyboard navigation " +
			"(Esc browse, Up/Down, Enter expand) works without it. " +
			"Set NABD_FORCE_TOUCH=1 to override"))
	}
	if err := doChatWithFeed(interactiveMode, *sessDir, *cont, *feedTouch); err != nil {
		die(err)
	}
}

// checkUISurface applies the ADR-0001 flag table for the collapsed
// v1.6.0+v1.7.0 landing: --ui always beats NABD_UI, the deprecated --feed
// alias maps onto --ui, an explicit conflict is refused, and the retired chat
// surface gets a migration message instead of a silent fall-through. It
// returns the surface to run, or an error the caller turns into exitUsage.
func checkUISurface(uiFlag string, useFeed bool, provided map[string]bool) (string, error) {
	uiGiven, feedGiven := provided["ui"], provided["feed"]

	if uiGiven {
		switch strings.ToLower(strings.TrimSpace(uiFlag)) {
		case uiFeed:
			// --feed=false asks for the retired surface, so it conflicts with
			// an explicit --ui=feed rather than being ignored.
			if feedGiven && !useFeed {
				return "", errors.New("--ui=feed conflicts with --feed=false; --feed is deprecated, use --ui=feed")
			}
			return uiFeed, nil
		case uiChat:
			return "", errors.New("--ui=chat is not available: the chat UI was retired (ADR-0001); use --ui=feed")
		default:
			return "", fmt.Errorf("--ui=%q is not a recognized surface; use --ui=feed", uiFlag)
		}
	}

	// No --ui: the deprecated alias decides what the operator asked for.
	if feedGiven {
		if !useFeed {
			return "", errors.New("--feed=false selected the chat UI, which was retired (ADR-0001); use --ui=feed")
		}
		fmt.Fprintln(os.Stderr, "nabd: --feed is deprecated; use --ui=feed")
		return uiFeed, nil
	}

	// Environment surface. An unknown or retired value warns and falls back to
	// the default instead of failing, so a stale .bashrc or wrapper script
	// cannot lock the operator out of the agent.
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("NABD_UI"))); v != "" && v != uiFeed {
		fmt.Fprintf(os.Stderr, "nabd: NABD_UI=%q is not a usable surface (feed is the only interactive UI); using feed\n", v)
	}
	return uiFeed, nil
}

// rejectUIFlagWith refuses --ui/--feed beside a non-interactive mode. ADR-0001
// asks for a rejected command line (exitUsage) rather than a flag that appears
// accepted and is then ignored.
func rejectUIFlagWith(replay, prompt, exportPath string, provided map[string]bool) {
	if !provided["ui"] && !provided["feed"] {
		return
	}
	var mode string
	switch {
	case replay != "":
		mode = "--replay"
	case prompt != "":
		mode = "-p"
	case exportPath != "":
		mode = "--export"
	default:
		return
	}
	fmt.Fprintf(os.Stderr, "nabd: --ui/--feed has no effect with %s; set NABD_UI for a persistent preference\n", mode)
	os.Exit(exitUsage)
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

func touchAllowed(termuxVersion, force string) bool {
	if termuxVersion != "" && force == "" {
		return false
	}
	return true
}

func doChatWithFeed(mode perm.Mode, dir string, cont bool, feedTouch bool) error {
	ui.SetLimitNotice(limitNoticeArabic)
	ui.SetCopyNotices(copySuccessArabic, copyUnavailableArabic, copyBlockedArabic)

	prov, err := pickProvider()
	if err != nil {
		return err
	}

	sess, err := newInteractiveSession(prov)
	if err != nil {
		return err
	}
	sess.SetMode(mode)
	root := sess.root

	var journalPath string
	var journal *store.JSONL
	if cont {
		journalPath, err = latestSession(dir, root.Dir())
		if err != nil {
			return err
		}
		journal, err = openSessionJournal(journalPath)
		if err == nil {
			writeSessionPolicyWarnings(os.Stderr, journalStoreOptions().Redact != nil)
		}
	} else {
		journal, journalPath, err = newSessionJournalWithWarning(dir, os.Stderr)
	}
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

	feed := ui.NewFeed()
	feed.SetTouch(feedTouch)
	feed.SetGitHeader(true)
	feed.SetGitDir(root.Dir())
	feed.SetPickerRoot(root.Dir())

	if cont {
		sess.loop.Seed(prevEvs)
	}

	batcher := ui.NewBatcher(eventBatchInterval, maxEventBatchSize, func(batch []agent.Event) {
		feed.SendBatch(batch)
	})
	batcher.Start()

	sess.loop.Sink = agent.Fanout{journal, feedSink{batcher: batcher}}

	feed.SetRunner(sess.loop)
	feed.SetApprover(sess.ap)
	feed.SetCallbacks(sess.callbacks())

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

	if err := sess.loop.Start(fmt.Sprintf("%s · %s · %s",
		build.BannerPrefix(), prov.Name(), filepath.Base(journalPath)), root.Dir()); err != nil {
		batcher.Stop()
		journal.Close()
		return err
	}

	if cont {
		noteMutationRecovery(sess.loop, sess.reg, prevEvs)
	}
	if s := conflictLine(config.Conflicts()); s != "" {
		sess.loop.Note(s)
	}

	// The batcher must outlive the interactive program: it carries every live
	// event, and Batcher.Add is a silent no-op once stopped. finishFeedSession
	// waits for the program to exit before stopping it, so nothing is dropped
	// while the session runs and nothing races the End marker. (Stopping right
	// after loop.Start here regressed exactly that: the feed showed nothing
	// past the banner.)
	return finishFeedSession(progDone, batcher, sess.loop, journal, journalPath)
}

// finishFeedSession is the feed path's shutdown sequence, isolated so its
// ordering is testable: wait for the interactive program to exit, stop the
// batcher so its final flush lands, then mark the session ended and close the
// journal. Stopping the batcher before the program exits silently drops the
// whole session's events (Batcher.Add no-ops once stopped); stopping it after
// loop.End lets events race the End marker.
func finishFeedSession(progDone <-chan error, batcher *ui.Batcher, loop *agent.Loop, journal io.Closer, journalPath string) error {
	if err := <-progDone; err != nil {
		batcher.Stop()
		journal.Close()
		return err
	}
	batcher.Stop()
	endErr := loop.End(fmt.Sprintf(statusSessionEnded, filepath.Base(journalPath)))
	closeErr := journal.Close()
	reportSession(os.Stdout, os.Stderr, journalPath, closeErr)
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
	var reverted, failed []string
	for _, r := range reg.PersistedUndo(recs, n) {
		mark := "x"
		if r.OK {
			mark = "ok"
			if r.Rel != "" {
				reverted = append(reverted, r.Rel)
			}
		} else if r.Rel != "" {
			failed = append(failed, r.Rel)
		}
		if r.Rel == "" {
			fmt.Fprintf(&b, "%s %s\n", mark, r.Note)
			continue
		}
		fmt.Fprintf(&b, "%s %s - %s\n", mark, r.Rel, r.Note)
	}
	s := strings.TrimRight(b.String(), "\n")
	loop.NoteUndo(fmt.Sprintf("/undo %d - %s", n, s), reverted, failed)
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

// reportSession prints the authoritative session path and routes a close
// failure to the error stream beside it. The path is printed unconditionally,
// so a failed close never hides where the full transcript was written, and
// the close error never replaces the path.
func reportSession(out, errOut io.Writer, path string, closeErr error) {
	fmt.Fprintln(out, "session:", path)
	if closeErr != nil {
		fmt.Fprintln(errOut, "nabd: session close:", closeErr)
	}
}

// userHomeDir is the single seam for resolving the operator's home
// directory. Tests replace it to prove that the default session directory is
// built in exactly one place.
var userHomeDir = os.UserHomeDir

// defaultSessionDir is the one source of the default session directory
// (~/.ag/sessions): it resolves the home directory and guarantees the
// directory exists with mode 0o700. A caller-supplied --dir never reaches
// here.
func defaultSessionDir() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".ag", "sessions")
	if err := ensureDefaultSessionDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func sessionPath(dir string) (string, error) {
	return sessionPathAt(dir, time.Now().UTC())
}

// newSessionJournalWithOptions allocates a new journal atomically, applying the
// supplied persistence options. Production new sessions pass the process
// redaction policy; --continue opens an existing file via openSessionJournal.
func newSessionJournalWithOptions(dir string, opts store.Options) (*store.JSONL, string, error) {
	const maxAttempts = 32
	for i := 0; i < maxAttempts; i++ {
		path, err := sessionPath(dir)
		if err != nil {
			return nil, "", err
		}
		journal, err := store.NewJSONLExclusiveWithOptions(path, opts)
		if err == nil {
			return journal, path, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("could not allocate a unique session journal after %d attempts", maxAttempts)
}

// sessionPathAt is the pure production naming helper behind sessionPath. It is
// the single source of truth for new-session journal filenames. Keeping it pure
// (dir + now -> path) lets tests exercise the real naming logic with a frozen
// clock without touching the filesystem clock or sleeping.
func sessionPathAt(dir string, now time.Time) (string, error) {
	if dir == "" {
		var err error
		dir, err = defaultSessionDir()
		if err != nil {
			return "", err
		}
	}
	base := now.Format("20060102-150405.000")
	name := newSessionName(base)
	return filepath.Join(dir, name), nil
}

// newSessionSuffix builds a random disambiguation suffix for a new-session
// journal. The random component avoids PID-namespace collisions; the PID and
// process-local counter remain a fallback if the system random source fails.
//
// The counter is zero-padded (%04d) so that lexicographic order of the suffix
// reflects counter order within one timestamp prefix up to 9999 allocations;
// beyond that the width grows.
func newSessionSuffix() string {
	var random [8]byte
	if _, err := crand.Read(random[:]); err == nil {
		return "-r" + hex.EncodeToString(random[:])
	}
	return fmt.Sprintf("-p%d-c%04d", os.Getpid(), newSessionCounter())
}

// sessionCounter provides process-local uniqueness. A package-global atomic is
// sufficient because uniqueness only needs to hold within one process-lifetime;
// cross-process uniqueness is provided by the PID component of the suffix.
var sessionCounter atomic.Uint64

func newSessionCounter() uint64 {
	return sessionCounter.Add(1)
}

// newSessionName assembles a new-session journal name from its timestamp base,
// appending the PID+counter suffix before the ".jsonl" extension.
func newSessionName(base string) string {
	return base + newSessionSuffix() + ".jsonl"
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

// pickProvider selects the interactive provider. NABD_PROVIDER is a free
// registry identifier: "router" selects the router, and every other value is
// resolved through the registry, so a provider a user adds to providers.json is
// selectable without a code change. An identifier the registry does not know is
// an error naming the file — never a silent fall-through to the
// credential-detection order below, which now runs only when nothing was named.
func pickProvider() (provider.Provider, error) {
	if err := config.Load(); err != nil {
		return nil, err
	}
	if id := strings.ToLower(strings.TrimSpace(config.Get("NABD_PROVIDER"))); id != "" {
		if id == "router" {
			return pickRouterProvider()
		}
		return provider.BuildStandaloneProvider(id)
	}

	if config.Has("GROQ_API_KEY") {
		return provider.BuildStandaloneProvider("groq")
	}
	if config.Has("OPENROUTER_API_KEY") {
		return provider.BuildStandaloneProvider("openrouter")
	}
	if config.Has("NVIDIA_API_KEY") {
		return provider.BuildStandaloneProvider("nvidia")
	}
	return provider.BuildStandaloneProvider("anthropic")
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

	// How long the router may honor a provider-supplied Retry-After before
	// declaring exhaustion. Zero (the default) keeps the historical behavior:
	// fall through the remaining routes and fail immediately.
	retryWaitSec, err := provider.ParseRetryAfterWait(config.Get("NABD_ROUTER_RETRY_AFTER_WAIT"))
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

	router, err := provider.NewRouter(routes, time.Duration(timeoutSec)*time.Second, provider.RealClock{})
	if err != nil {
		return nil, err
	}
	return router.WithRetryAfterWait(time.Duration(retryWaitSec) * time.Second), nil
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
		var err error
		sessDir, err = defaultSessionDir()
		if err != nil {
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
	seen := map[string]bool{}
	aborted := map[string]bool{}
	key := func(rec *agent.EditRecord) string {
		if rec == nil {
			return ""
		}
		if rec.MutationID != "" {
			return rec.MutationID
		}
		return rec.Path + "\x00" + rec.HashBefore + "\x00" + rec.HashAfter + "\x00" + rec.BlobAfter
	}
	for i := len(evs) - 1; i >= 0; i-- {
		rec := evs[i].Edit
		switch evs[i].Type {
		case agent.EventEditAbort:
			if rec != nil {
				aborted[key(rec)] = true
			}
		case agent.EventEdit, agent.EventEditIntent:
			if rec == nil {
				continue
			}
			k := key(rec)
			if aborted[k] || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, rec)
		}
	}
	return out
}
