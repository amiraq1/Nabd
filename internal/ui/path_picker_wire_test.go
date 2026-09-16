package ui

import (
	"os"
	"path/filepath"
	"slices"
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
	const wantPrefix = "cannot index files for @: "
	if !strings.HasPrefix(bad.status, wantPrefix) {
		t.Fatalf("status = %q, want prefix %q", bad.status, wantPrefix)
	}
	if !bad.pickerScanned {
		t.Fatal("a failed scan must be remembered, or every keystroke retries the walk")
	}
}

// TestPickerExplicitSessionRootOverridesGitDir verifies Scenario 1:
// Explicit session root => indexing occurs under session root, not gitDir.
func TestPickerExplicitSessionRootOverridesGitDir(t *testing.T) {
	gitDir := t.TempDir()
	sessionDir := t.TempDir()

	gitFile := filepath.Join(gitDir, "git_exclusive.txt")
	if err := os.WriteFile(gitFile, []byte("git"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(sessionDir, "session_exclusive.txt")
	if err := os.WriteFile(sessionFile, []byte("session"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewFeed()
	m.composer.focus()
	m.SetGitDir(gitDir)
	m.SetPickerRoot(sessionDir)

	m.composer.setValue("@session")
	m.syncPathPicker()

	if !m.pickerVisible() {
		t.Fatal("expected picker popup to be visible for explicit session root")
	}
	curr, ok := m.picker.currentPath()
	if !ok || curr != "session_exclusive.txt" {
		t.Fatalf("currentPath = %q (ok=%v), want %q", curr, ok, "session_exclusive.txt")
	}
	if !slices.Contains(m.pickerIndex.paths, "session_exclusive.txt") {
		t.Fatalf("pickerIndex.paths missing session_exclusive.txt: %v", m.pickerIndex.paths)
	}
	if slices.Contains(m.pickerIndex.paths, "git_exclusive.txt") {
		t.Fatalf("pickerIndex.paths must NOT contain git_exclusive.txt when session root is set: %v", m.pickerIndex.paths)
	}

	// Query matching git-only file must yield no results and close the picker.
	m.composer.setValue("@git_ex")
	m.syncPathPicker()
	if m.pickerVisible() {
		t.Fatal("picker should close when query only matches files outside the session root")
	}
}

// TestPickerNoSessionRootFallsBackToGitDir verifies Scenario 2:
// No session root => fallback to gitDir (no regression).
func TestPickerNoSessionRootFallsBackToGitDir(t *testing.T) {
	gitDir := t.TempDir()
	gitFile := filepath.Join(gitDir, "fallback_repo_file.go")
	if err := os.WriteFile(gitFile, []byte("package test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewFeed()
	m.composer.focus()
	m.SetGitDir(gitDir)
	if m.pickerRoot != "" {
		t.Fatalf("expected m.pickerRoot to be empty before SetPickerRoot, got %q", m.pickerRoot)
	}

	m.composer.setValue("@fallback")
	m.syncPathPicker()

	if !m.pickerVisible() {
		t.Fatal("expected picker popup to be visible via gitDir fallback")
	}
	curr, ok := m.picker.currentPath()
	if !ok || curr != "fallback_repo_file.go" {
		t.Fatalf("currentPath = %q (ok=%v), want %q", curr, ok, "fallback_repo_file.go")
	}
	if !slices.Contains(m.pickerIndex.paths, "fallback_repo_file.go") {
		t.Fatalf("pickerIndex.paths missing fallback_repo_file.go: %v", m.pickerIndex.paths)
	}
	if m.pickerErr != "" {
		t.Fatalf("unexpected pickerErr on valid gitDir fallback: %q", m.pickerErr)
	}

	// When neither session root nor gitDir is configured, picker should fail
	// cleanly with the designated error message without crashing.
	noRootFeed := NewFeed()
	noRootFeed.composer.focus()
	noRootFeed.composer.setValue("@")
	noRootFeed.syncPathPicker()
	if noRootFeed.pickerVisible() {
		t.Fatal("picker must not open when neither pickerRoot nor gitDir is configured")
	}
	if got, want := noRootFeed.pickerErr, "no directory to index for @"; got != want {
		t.Fatalf("pickerErr = %q, want %q", got, want)
	}
}

// TestPickerUnreadableSessionRootReportsStatusWithoutCrash verifies Scenario 3:
// Unreadable session root => produces error message prefix "cannot index files for @: "
// and sets status without crashing/panicking.
func TestPickerUnreadableSessionRootReportsStatusWithoutCrash(t *testing.T) {
	unreadableRoot := filepath.Join(t.TempDir(), "nonexistent_dir")
	m := NewFeed()
	m.composer.focus()
	m.SetPickerRoot(unreadableRoot)

	// Triggering @ must not panic or crash.
	m.composer.setValue("@foo")
	m.syncPathPicker()

	if m.pickerVisible() {
		t.Fatal("picker popup must remain closed for unreadable session root")
	}
	const wantPrefix = "cannot index files for @: "
	if !strings.HasPrefix(m.status, wantPrefix) {
		t.Fatalf("status = %q, want prefix %q", m.status, wantPrefix)
	}
	if !strings.HasPrefix(m.pickerErr, wantPrefix) {
		t.Fatalf("pickerErr = %q, want prefix %q", m.pickerErr, wantPrefix)
	}
	if !m.pickerScanned {
		t.Fatal("pickerScanned must be true to avoid repeated scans on subsequent keystrokes")
	}

	// Subsequent keystrokes should maintain status and not re-scan or panic.
	m.composer.setValue("@foobar")
	m.syncPathPicker()
	if m.pickerVisible() {
		t.Fatal("picker popup must remain closed on subsequent keystroke")
	}
	if !strings.HasPrefix(m.status, wantPrefix) {
		t.Fatalf("status after edit = %q, want prefix %q", m.status, wantPrefix)
	}

	// Even with a valid gitDir configured, an explicit unreadable session root
	// must report the error rather than silently falling back.
	validGitDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(validGitDir, "git.txt"), []byte("g"), 0o644); err != nil {
		t.Fatal(err)
	}
	m2 := NewFeed()
	m2.composer.focus()
	m2.SetGitDir(validGitDir)
	m2.SetPickerRoot(unreadableRoot)
	m2.composer.setValue("@foo")
	m2.syncPathPicker()
	if m2.pickerVisible() {
		t.Fatal("picker popup must remain closed when explicit session root is unreadable")
	}
	if !strings.HasPrefix(m2.status, wantPrefix) {
		t.Fatalf("status = %q, want prefix %q", m2.status, wantPrefix)
	}
}
