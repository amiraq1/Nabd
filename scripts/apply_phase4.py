from pathlib import Path


def replace_once(path: str, old: str, new: str) -> None:
    p = Path(path)
    text = p.read_text()
    if old not in text:
        raise SystemExit(f"missing marker in {path}: {old!r}")
    p.write_text(text.replace(old, new, 1))


replace_once("internal/tools/grep.go", '"fmt"\n\t"io/fs"', '"fmt"\n\t"io"\n\t"io/fs"')
replace_once("internal/tools/grep.go", 'return 0, fmt.Errorf("EOF")', "return 0, io.EOF")
replace_once("internal/tools/write_commit.go", '\t"path/filepath"\n', "")
replace_once("internal/tools/write_commit.go", "if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {", "if err := mkdirParentDirs(abs); err != nil {")
replace_once("internal/tools/write_diff.go", '"strings"\n)', '"strings"\n\t"sync"\n)')
replace_once("internal/tools/write_diff.go", "var (\n\t// maxDiffLines", "var diffWorkMu sync.Mutex\n\nvar (\n\t// maxDiffLines")
needle = "\t// LCS table for the two line sequences. Cancellation is checked once per\n"
insert = "\t// Serialize matrix allocation across calls so maxDiffCells is an aggregate process ceiling.\n\tdiffWorkMu.Lock()\n\tdefer diffWorkMu.Unlock()\n\tif err := ctx.Err(); err != nil {\n\t\treturn \"\", err\n\t}\n\n"
replace_once("internal/tools/write_diff.go", needle, insert + needle)
replace_once("internal/provider/router.go", "KNOWN_LIMITATION_CIRCUIT_BREAKER: NOT_IMPLEMENTED (P section)", "CIRCUIT_BREAKER: IMPLEMENTED — 401/403 opens only that route for a five-minute cooldown; the next request after expiry is the half-open probe")
for name in ["internal/snap/rename_windows.go", "internal/snap/sync_windows.go", "internal/config/open_windows.go", "internal/config/owner_other.go"]:
    p = Path(name)
    text = p.read_text()
    note = "// Windows compatibility shim: compiled in CI, but Windows is not a supported runtime target.\n"
    if note not in text:
        marker = text.find("\n\n")
        p.write_text(text[: marker + 2] + note + text[marker + 2 :])
replace_once(".github/workflows/ci.yml", "          - { goos: android, goarch: arm64, vet: true }", "          - { goos: android, goarch: arm64, vet: true }\n          - { goos: windows, goarch: amd64, vet: false }")
Path("internal/tools/mkdir_mode.go").write_text('''package tools

import (
    "fmt"
    "os"
    "path/filepath"
)

// mkdirParentDirs inherits the nearest existing parent directory mode.
func mkdirParentDirs(abs string) error {
    target := filepath.Dir(abs)
    if fi, err := os.Stat(target); err == nil {
        if !fi.IsDir() { return fmt.Errorf("parent is not a directory: %s", target) }
        return nil
    } else if !os.IsNotExist(err) { return err }
    ancestor := target
    for {
        parent := filepath.Dir(ancestor)
        fi, err := os.Stat(parent)
        if err == nil {
            if !fi.IsDir() { return fmt.Errorf("ancestor is not a directory: %s", parent) }
            mode := fi.Mode().Perm()
            if mode == 0 { mode = 0o755 }
            return os.MkdirAll(target, mode)
        }
        if !os.IsNotExist(err) { return err }
        if parent == ancestor { return os.MkdirAll(target, 0o755) }
        ancestor = parent
    }
}
''')
Path("internal/tools/hygiene_test.go").write_text('''package tools

import (
    "io"
    "os"
    "path/filepath"
    "testing"
)

func TestMkdirParentDirsInheritsPrivateParentMode(t *testing.T) {
    root := t.TempDir()
    private := filepath.Join(root, "private")
    if err := os.Mkdir(private, 0o700); err != nil { t.Fatal(err) }
    target := filepath.Join(private, "a", "b", "file.txt")
    if err := mkdirParentDirs(target); err != nil { t.Fatal(err) }
    for _, dir := range []string{filepath.Join(private, "a"), filepath.Join(private, "a", "b")} {
        fi, err := os.Stat(dir)
        if err != nil { t.Fatal(err) }
        if got := fi.Mode().Perm(); got != 0o700 { t.Fatalf("%s mode=%04o, want 0700", dir, got) }
    }
}

func TestLimitedReaderUsesIOEOF(t *testing.T) {
    f, err := os.CreateTemp(t.TempDir(), "empty")
    if err != nil { t.Fatal(err) }
    defer f.Close()
    r := &limitedReader{f: f, left: 0}
    _, err = r.Read(make([]byte, 1))
    if err != io.EOF { t.Fatalf("error=%v, want io.EOF", err) }
}
''')
for name in [".github/workflows/go-checks.yml", ".github/workflows/phase4-apply.yml", ".github/workflows/phase4-apply-v2.yml", ".github/workflows/phase4-apply-v3.yml", "scripts/apply_phase4.py"]:
    Path(name).unlink(missing_ok=True)
