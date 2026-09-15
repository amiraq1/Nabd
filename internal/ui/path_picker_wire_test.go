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

// TestPickerRowsAreAccountedForAtEveryHeight is the frame contract: whatever
// the layout reserves, view() must emit exactly that many rows, and the
// composed frame must never exceed the terminal. The popup is transient
// chrome, so below the shared popup floor (menuMinRows) it is dropped rather
// than compressed into rows view() cannot honour.
func TestPickerRowsAreAccountedForAtEveryHeight(t *testing.T) {
	m := pickerFeed(t)
	m.composer.setValue("@")
	m.syncPathPicker()
	if !m.pickerVisible() {
		t.Fatal("a bare @ must open the popup")
	}
	if m.computeLayout().PickerRows <= 0 {
		t.Fatal("PickerRows must be counted at a normal height, or the frame overflows")
	}

	for _, h := range []int{3, 4, 5, 6, 8, 12, 24, 40} {
		m.height = h
		lm := m.computeLayout()
		if lm.PickerRows > 0 {
			if lm.PickerRows < menuMinRows {
				t.Fatalf("height %d: PickerRows = %d, below the popup floor %d", h, lm.PickerRows, menuMinRows)
			}
			rows := strings.Count(m.picker.view(lm.TerminalWidth, lm.PickerRows), "\n") + 1
			if rows != lm.PickerRows {
				t.Fatalf("height %d: the popup emitted %d rows but the layout reserved %d", h, rows, lm.PickerRows)
			}
		}
		if got := strings.Count(m.View(), "\n") + 1; got > h {
			t.Fatalf("height %d: View emitted %d rows", h, got)
		}
	}

	// At three rows the composer and footer alone leave less than the floor,
	// so the popup must be dropped outright.
	m.height = 3
	if rows := m.computeLayout().PickerRows; rows != 0 {
		t.Fatalf("PickerRows = %d on a 3-row terminal, want it dropped", rows)
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
