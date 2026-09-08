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
