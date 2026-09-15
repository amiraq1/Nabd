package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// pickerFeed returns a focused Feed rooted at a small tree.
func pickerFeed(t *testing.T) *Feed {
	t.Helper()
	dir := t.TempDir()
	for _, p := range []string{
		"internal/pathindex/scan.go",
		"internal/ui/composer.go",
		"docs/scanning.md",
	} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := NewFeed()
	m.composer.focus()
	m.SetPickerRoot(dir)
	return m
}

func TestPickerOpensOnAnOpenAtReference(t *testing.T) {
	m := pickerFeed(t)
	m.composer.setValue("@scan")
	m.syncPathPicker()
	if !m.pickerVisible() {
		t.Fatal("@scan must open the popup")
	}
	got, ok := m.picker.currentPath()
	if !ok || got != "docs/scanning.md" {
		t.Fatalf("currentPath = %q (ok=%v), want the best-ranked docs/scanning.md", got, ok)
	}

	// A completed reference closes the popup again.
	m.composer.setValue("@scan done")
	m.syncPathPicker()
	if m.pickerVisible() {
		t.Fatal("a closed token must close the popup")
	}
}

func TestPickerEnterCompletesAndNeverSends(t *testing.T) {
	m := pickerFeed(t)
	m.composer.setValue("read @composer")
	m.syncPathPicker()
	if !m.pickerVisible() {
		t.Fatal("expected the popup")
	}
	m.routeKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.busy || m.running {
		t.Fatal("Enter completed a path; it must never start a run")
	}
	if got, want := m.composer.value(), "read @internal/ui/composer.go "; got != want {
		t.Fatalf("composer = %q, want %q", got, want)
	}
	if m.pickerVisible() {
		t.Fatal("the popup must close after completing")
	}
}

func TestPickerEscClosesWithoutLeavingTheComposer(t *testing.T) {
	m := pickerFeed(t)
	m.composer.setValue("@scan")
	m.syncPathPicker()
	m.routeKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.pickerVisible() {
		t.Fatal("Esc must close the popup")
	}
	if m.navigationMode {
		t.Fatal("Esc dismissed the popup only; browse mode is one Esc further")
	}
	if m.composer.value() != "@scan" {
		t.Fatalf("composer = %q, want the typed text untouched", m.composer.value())
	}
}

func TestPickerNeverCompetesWithTheSlashMenuOrTheModal(t *testing.T) {
	m := pickerFeed(t)
	m.composer.setValue("/he")
	m.syncSlashMenu()
	m.syncPathPicker()
	if !m.menu.visible {
		t.Fatal("expected the slash menu for /he")
	}
	if m.pickerVisible() {
		t.Fatal("two popups must never be open at once")
	}

	m.menu.close()
	m.composer.setValue("@scan")
	m.modalVisible = true
	m.syncPathPicker()
	if m.pickerVisible() {
		t.Fatal("a permission decision owns the keyboard; the popup must stay closed")
	}
}

func TestSendClosesThePicker(t *testing.T) {
	m := pickerFeed(t)
	m.composer.setValue("@scan")
	m.syncPathPicker()
	m.trySend()
	if m.pickerVisible() {
		t.Fatal("a send must never leave a popup floating over the composer")
	}
}

func TestPickerRowsAreAccountedForAndDroppedBelowTheFloor(t *testing.T) {
	m := pickerFeed(t)
	m.composer.setValue("@")
	m.syncPathPicker()
	if !m.pickerVisible() {
		t.Fatal("a bare @ must open the popup")
	}

	lm := m.computeLayout()
	if lm.PickerRows <= 0 {
		t.Fatal("PickerRows must be counted, or the frame overflows")
	}
	if rows := strings.Count(m.picker.view(lm.TerminalWidth, lm.PickerRows), "\n") + 1; rows != lm.PickerRows {
		t.Fatalf("the popup emitted %d rows but the layout reserved %d", rows, lm.PickerRows)
	}
	if got := strings.Count(m.View(), "\n") + 1; got > m.height {
		t.Fatalf("View emitted %d rows for a %d-row terminal", got, m.height)
	}

	// A terminal too short for the popup drops it rather than compressing it
	// into rows view() cannot honour.
	m.height = 5
	if rows := m.computeLayout().PickerRows; rows != 0 {
		t.Fatalf("PickerRows = %d on a 5-row terminal, want it dropped", rows)
	}
	if got := strings.Count(m.View(), "\n") + 1; got > m.height {
		t.Fatalf("View emitted %d rows for a %d-row terminal", got, m.height)
	}
}

// TestPickerFooterDoesNotAdvertiseSend is the honesty contract for the footer:
// while the popup owns Enter, a footer reading "Enter send" would print a
// false instruction.
func TestPickerFooterDoesNotAdvertiseSend(t *testing.T) {
	m := pickerFeed(t)
	m.composer.setValue("@scan")
	m.syncPathPicker()
	for _, w := range []int{120, 80, 60, 40, 24} {
		footer := m.footerText(w)
		if strings.Contains(footer, "Enter send") {
			t.Fatalf("footer at width %d says %q while the popup owns Enter", w, footer)
		}
	}
}

func TestPickerScansOnceAndReportsAnUnusableRoot(t *testing.T) {
	m := pickerFeed(t)
	m.composer.setValue("@scan")
	m.syncPathPicker()
	first := m.pickerIndex.paths
	if len(first) == 0 {
		t.Fatal("expected an index")
	}
	// A new file after the first scan is deliberately not picked up: the
	// index is cached for the session.
	m.composer.setValue("@scanning")
	m.syncPathPicker()
	if len(m.pickerIndex.paths) != len(first) {
		t.Fatal("the index must be scanned once per root, not per keystroke")
	}

	bad := NewFeed()
	bad.composer.focus()
	bad.SetPickerRoot(filepath.Join(t.TempDir(), "missing"))
	bad.composer.setValue("@x")
	bad.syncPathPicker()
	if bad.pickerVisible() {
		t.Fatal("an unreadable root must not show an empty popup")
	}
	if bad.status == "" {
		t.Fatal("an unreadable root must be reported, not swallowed")
	}
	if !bad.pickerScanned {
		t.Fatal("a failed scan must be remembered, or every keystroke retries the walk")
	}
}
