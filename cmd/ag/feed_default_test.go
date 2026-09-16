package main

import (
	"os"
	"strings"
	"testing"
)

// Feed is the default interactive UI: `nabd` with no flags runs doChatWithFeed,
// and `--feed=false` is the operator's rollback to the legacy chat UI. Both
// halves are security-relevant, because the feed path is the one that wires the
// @ path picker to the session root and therefore indexes the tree without
// being asked for it. A silent flip of this default, in either direction,
// changes what a bare `nabd` reads.
//
// The flag is declared inside main(), so the default is asserted against the
// source of cmd/ag/main.go rather than by parsing a flag set. This proves the
// declaration, not the runtime behaviour of the process; the wiring it guards
// is covered by TestPickerExplicitSessionRootOverridesGitDir and its neighbours
// in internal/ui.
func TestFeedIsTheDefaultInteractiveUI(t *testing.T) {
	data, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	src := string(data)

	if !strings.Contains(src, `flag.Bool("feed", true,`) {
		t.Error(`-feed must default to true: a bare "nabd" runs the feed UI`)
	}
	if !strings.Contains(src, "set --feed=false for the legacy chat UI") {
		t.Error("the -feed help text must name --feed=false as the rollback path")
	}

	if !strings.Contains(src, "if *useFeed {") {
		t.Error("the two interactive entry points must stay behind one *useFeed branch")
	}
	if !strings.Contains(src, "doChatWithFeed(interactiveMode") {
		t.Error("main must reach doChatWithFeed when -feed is set")
	}
	if !strings.Contains(src, "doChat(interactiveMode") {
		t.Error("--feed=false must still reach the legacy doChat entry point")
	}
}
