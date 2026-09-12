package ui

import (
	"testing"

	"nabd/internal/agent"
	"nabd/internal/presentation"
)

// seedFeed establishes a baseline render signature via applyBatch so the
// tests below can observe the dirty return of subsequent refresh() calls
// against a valid baseline (the first refresh marks an invalid baseline
// dirty by design).
func seedFeed(t *testing.T, events ...agent.Event) *Feed {
	t.Helper()
	f := NewFeed()
	f.width = 80
	f.height = 24
	f.applyBatch(events)
	return f
}

// TestRefreshDirtyIdentical: an unchanged refresh reports false.
func TestRefreshDirtyIdentical(t *testing.T) {
	f := seedFeed(t,
		agent.Event{Seq: 1, Type: agent.UserMsg, Text: "hello"},
		agent.Event{Seq: 2, Type: agent.TurnEnd},
	)
	if got := f.refresh(); got {
		t.Fatal("identical refresh must return false")
	}
	// Still false after another identical pass.
	if got := f.refresh(); got {
		t.Fatal("repeated identical refresh must return false")
	}
}

// TestRefreshDirtyStreamingGrowth: growing the last assistant item is dirty.
func TestRefreshDirtyStreamingGrowth(t *testing.T) {
	f := seedFeed(t,
		agent.Event{Seq: 1, Type: agent.UserMsg, Text: "say hello"},
		agent.Event{Seq: 2, Type: agent.TextDelta, Text: "hel"},
	)
	if err := f.proj.Apply(agent.Event{Seq: 3, Type: agent.TextDelta, Text: "lo world"}); err != nil {
		t.Fatal(err)
	}
	if got := f.refresh(); !got {
		t.Fatal("streaming growth must return true")
	}
}

// TestRefreshDirtyAddDeleteReorder: adding, deleting and reordering items
// that change the final rendered output report true.
func TestRefreshDirtyAddDeleteReorder(t *testing.T) {
	// ADD: a new distinct message appears.
	f := seedFeed(t,
		agent.Event{Seq: 1, Type: agent.UserMsg, Text: "alpha"},
	)
	if err := f.proj.Apply(agent.Event{Seq: 2, Type: agent.UserMsg, Text: "bravo"}); err != nil {
		t.Fatal(err)
	}
	if got := f.refresh(); !got {
		t.Fatal("adding a visible item must return true")
	}

	// DELETE: moving to an empty feed changes the output.
	f.proj = presentation.NewProjector()
	if got := f.refresh(); !got {
		t.Fatal("deleting all items (to empty frame) must return true")
	}

	// REORDER: identical item set, different final order, distinct contents.
	// Both items share the same Seq so sortBySeq is stable and preserves
	// insertion order, letting the reverse insertion change the output.
	f = NewFeed()
	f.width = 80
	f.height = 24
	f.applyBatch([]agent.Event{
		{Seq: 5, Type: agent.UserMsg, Text: "alpha"},
		{Seq: 5, Type: agent.UserMsg, Text: "bravo"},
	})
	f.proj = presentation.NewProjector()
	_ = f.proj.Apply(agent.Event{Seq: 5, Type: agent.UserMsg, Text: "bravo"})
	_ = f.proj.Apply(agent.Event{Seq: 5, Type: agent.UserMsg, Text: "alpha"})
	if got := f.refresh(); !got {
		t.Fatal("reordering distinct items must return true")
	}
}

// TestRefreshDirtySeqOnly: changing only Seq (visible output identical)
// must not report dirty.
func TestRefreshDirtySeqOnly(t *testing.T) {
	f := seedFeed(t,
		agent.Event{Seq: 1, Type: agent.UserMsg, Text: "hello"},
	)
	f.proj = presentation.NewProjector()
	if err := f.proj.Apply(agent.Event{Seq: 999, Type: agent.UserMsg, Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if got := f.refresh(); got {
		t.Fatal("Seq-only change must return false (output identical)")
	}
}

// TestRefreshDirtyIDOnly: changing only ID (visible output identical) must
// not report dirty.
func TestRefreshDirtyIDOnly(t *testing.T) {
	f := seedFeed(t,
		agent.Event{Seq: 1, Type: agent.UserMsg, Text: "hello"},
	)
	// Same visible content, different ID (the projector keys the ID off Seq).
	f.proj = presentation.NewProjector()
	if err := f.proj.Apply(agent.Event{Seq: 42, Type: agent.UserMsg, Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if got := f.refresh(); got {
		t.Fatal("ID-only change must return false (output identical)")
	}
}

// TestRefreshDirtyWidthChangesWrapping: a width change that alters wrapping
// changes the final output and reports true.
func TestRefreshDirtyWidthChangesWrapping(t *testing.T) {
	long := "this is a fairly long single-line message that will exceed forty columns and wrap there but not at ninety columns"
	f := seedFeed(t,
		agent.Event{Seq: 1, Type: agent.UserMsg, Text: long},
	)

	f.width = 40
	if got := f.refresh(); !got {
		t.Fatal("width narrowing that rewraps lines must return true")
	}
}

// TestRefreshDirtyWidthSameVisualRows: a width change that leaves the final
// rendered output identical reports false. Notices are not padded to the
// terminal width, so short notice text renders identically at both widths.
func TestRefreshDirtyWidthSameVisualRows(t *testing.T) {
	f := seedFeed(t,
		agent.Event{Seq: 1, Type: agent.Notice, Text: "short text"},
	)

	f.width = 90
	if got := f.refresh(); got {
		t.Fatal("width change with identical output must return false")
	}
}

// TestRefreshDirtyEmptyAndDuplicateIDs: empty or duplicate feed-item IDs must
// never force a permanent true from refresh; the criterion is the final lines.
func TestRefreshDirtyEmptyAndDuplicateIDs(t *testing.T) {
	f := seedFeed(t,
		agent.Event{Seq: 1, Type: agent.UserMsg, Text: "hello"},
	)

	// Inject notices with empty and duplicate IDs via the UI path.
	f.notices = []presentation.FeedItem{
		{Type: presentation.ItemNotice, Text: "first notice", Seq: 5},
		{Type: presentation.ItemNotice, Text: "second notice", Seq: 6, ID: "dup"},
		{Type: presentation.ItemNotice, Text: "third notice", Seq: 7, ID: "dup"},
	}
	if got := f.refresh(); !got {
		t.Fatal("adding new notices must be dirty")
	}
	// The same notices again: identical output, so not dirty — empty and
	// duplicate IDs must not make every refresh dirty.
	if got := f.refresh(); got {
		t.Fatal("identical output with empty/duplicate IDs must return false")
	}
}

// TestRefreshDirtyEmptyFrame: the empty frame and transitions to/from it.
func TestRefreshDirtyEmptyFrame(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	// First refresh: invalid baseline is dirty by design.
	if got := f.refresh(); !got {
		t.Fatal("first refresh (invalid baseline) must return true")
	}
	// Empty frame, already validated: clean.
	if got := f.refresh(); got {
		t.Fatal("second refresh on empty frame must return false")
	}

	// Empty -> content.
	if err := f.proj.Apply(agent.Event{Seq: 1, Type: agent.UserMsg, Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if got := f.refresh(); !got {
		t.Fatal("transition from empty frame to content must return true")
	}

	// Content -> empty.
	f.proj = presentation.NewProjector()
	if got := f.refresh(); !got {
		t.Fatal("transition from content to empty frame must return true")
	}
	// Settled empty: clean again.
	if got := f.refresh(); got {
		t.Fatal("settled empty frame must return false")
	}
}

// TestRenderedLinesFingerprintPreservesBoundaries: length-prefixing must make
// ["ab","c"] differ from ["a","bc"].
func TestRenderedLinesFingerprintPreservesBoundaries(t *testing.T) {
	a := renderedLinesFingerprint([]string{"ab", "c"})
	b := renderedLinesFingerprint([]string{"a", "bc"})
	if a == b {
		t.Fatal("fingerprint must distinguish [ab,c] from [a,bc]")
	}
}

// TestRenderedLinesFingerprintDeterministic: the fingerprint is stable across
// calls for identical input, including empty lines.
func TestRenderedLinesFingerprintDeterministic(t *testing.T) {
	lines := []string{"first", "second", ""}
	first := renderedLinesFingerprint(lines)
	for i := 0; i < 100; i++ {
		if got := renderedLinesFingerprint(lines); got != first {
			t.Fatalf("fingerprint non-deterministic: %d vs %d", got, first)
		}
	}
	// The empty slice is also stable and distinct from a single empty line.
	if renderedLinesFingerprint(nil) != renderedLinesFingerprint([]string{}) {
		t.Fatal("nil and empty slice must agree")
	}
	if renderedLinesFingerprint([]string{}) == renderedLinesFingerprint([]string{""}) {
		t.Fatal("0 lines must differ from 1 empty line")
	}
}
