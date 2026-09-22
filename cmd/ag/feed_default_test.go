package main

import (
	"os"
	"strings"
	"testing"
)

// Feed is the only interactive UI: `nabd` with no flags runs doChatWithFeed,
// --ui=feed selects it explicitly, and the Chat surface that --feed=false used
// to reach was retired by ADR-0001. Both halves are security-relevant, because
// the feed path is the one that wires the @ path picker to the session root and
// therefore indexes the tree without being asked for it. A silent change of
// which surface runs changes what a bare `nabd` reads.
//
// The flags are declared inside main(), so the surface is asserted against the
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

	// ADR-0001: --ui is the surface flag and feed is the surface it names.
	if !strings.Contains(src, `flag.String("ui", "",`) {
		t.Error("--ui must be declared: it is the ADR-0001 interactive surface flag")
	}
	if !strings.Contains(src, "doChatWithFeed(interactiveMode") {
		t.Error("main must reach doChatWithFeed for the interactive run")
	}

	// --feed survives as a deprecated alias that points at --ui, so a script
	// that still passes it gets a migration hint instead of a flag error.
	if !strings.Contains(src, `flag.Bool("feed", true,`) {
		t.Error("--feed must stay declared as the deprecated alias (ADR-0001 v1.7.0 stub)")
	}
	if !strings.Contains(src, "deprecated: use --ui=feed") {
		t.Error("the --feed help text must name --ui as its replacement")
	}

	// The Chat entry point is gone: a second interactive surface would
	// silently reintroduce the divergent-prompt risk newSessionLoop exists to
	// prevent.
	if strings.Contains(src, "func doChat(") {
		t.Error("doChat must not exist: the chat surface was retired by ADR-0001")
	}
}
