package ui

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

// corpusWidth is DefaultWidth (50), strictly below the 60 clamp.
// It exercises the narrow-column layout that is the product's actual mobile target.
const corpusWidth = 50

var (
	corpusBinaryOnce sync.Once
	corpusBinaryPath string
	corpusBinaryErr  error
)

// getOrBuildCorpusBinary compiles nabd from the current working tree into a temporary directory,
// preventing any dependency on an existing PATH binary.
func getOrBuildCorpusBinary(t *testing.T) string {
	t.Helper()
	corpusBinaryOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "nabd_corpus_bin_*")
		if err != nil {
			corpusBinaryErr = err
			return
		}
		binPath := filepath.Join(tmpDir, "nabd")
		cmd := exec.Command("go", "build", "-o", binPath, "./cmd/ag/")
		cmd.Dir = "../.."
		if out, err := cmd.CombinedOutput(); err != nil {
			corpusBinaryErr = fmt.Errorf("building binary: %v\n%s", err, string(out))
			return
		}
		corpusBinaryPath = binPath
	})
	if corpusBinaryErr != nil {
		t.Fatalf("failed to build binary for corpus replay: %v", corpusBinaryErr)
	}
	return corpusBinaryPath
}

// runReplayInPTY executes the compiled binary under a real PTY with pinned environment and dimensions,
// interpreting the output through a virtual terminal emulator (vt10x) with 400 rows to capture
// complete screen contents without line-split or scrolling artifacts.
func runReplayInPTY(t *testing.T, binPath, journalPath string, envOverrides map[string]string) string {
	t.Helper()

	info, err := os.Stat(journalPath)
	if err != nil {
		t.Fatalf("stat journal %s: %v", journalPath, err)
	}
	if info.Size() == 0 {
		return "empty journal (0 events)"
	}

	cmd := exec.Command(binPath, "-replay", journalPath, "-speed", "0")

	// Pinned ambient environment
	pinnedEnv := map[string]string{
		"TERM":   "xterm-256color",
		"HOME":   "/corpus/home",
		"TZ":     "UTC",
		"LANG":   "C.UTF-8",
		"LC_ALL": "C.UTF-8",
		"PATH":   os.Getenv("PATH"),
	}
	for k, v := range envOverrides {
		if v == "" {
			delete(pinnedEnv, k)
		} else {
			pinnedEnv[k] = v
		}
	}
	envSlice := make([]string, 0, len(pinnedEnv))
	for k, v := range pinnedEnv {
		envSlice = append(envSlice, k+"="+v)
	}
	cmd.Env = envSlice

	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	ws := &pty.Winsize{Rows: 400, Cols: corpusWidth}
	_ = pty.Setsize(master, ws)
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	if err := cmd.Start(); err != nil {
		_ = master.Close()
		_ = slave.Close()
		t.Fatalf("cmd.Start: %v", err)
	}
	_ = slave.Close()

	var buf bytes.Buffer
	doneCopy := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, master)
		close(doneCopy)
	}()

	_ = cmd.Wait()
	_ = master.Close()
	<-doneCopy

	raw := buf.Bytes()
	vt := vt10x.New(vt10x.WithSize(corpusWidth, 400))
	_, _ = vt.Write(raw)

	var rows []string
	lastRow := -1
	for y := 0; y < 400; y++ {
		var line strings.Builder
		for x := 0; x < corpusWidth; x++ {
			c := vt.Cell(x, y)
			if c.Char != 0 {
				line.WriteRune(c.Char)
			} else {
				line.WriteByte(' ')
			}
		}
		trimmed := strings.TrimRight(ansi.Strip(line.String()), " ")
		if trimmed != "" && !strings.HasPrefix(trimmed, "replay ") {
			lastRow = y
			rows = append(rows, trimmed)
		}
	}

	if lastRow >= 390 {
		t.Fatalf("output reached row %d (>= 390); terminal height 400 exhausted and scrolling may occur", lastRow)
	}

	return strings.Join(rows, "\n")
}

// TestReplayCorpusGolden matches every corpus scenario against its committed golden text and sha256 index.
// If UPDATE_GOLDEN=1 is set, it updates the golden files and index.
func TestReplayCorpusGolden(t *testing.T) {
	binPath := getOrBuildCorpusBinary(t)
	corpusDir := "../../testdata/replay-corpus"

	files, err := filepath.Glob(filepath.Join(corpusDir, "*.jsonl"))
	if err != nil {
		t.Fatalf("globbing corpus files: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no .jsonl files found in testdata/replay-corpus")
	}
	sort.Strings(files)

	updateGolden := os.Getenv("UPDATE_GOLDEN") == "1"
	indexMap := make(map[string]string)
	indexFile := filepath.Join(corpusDir, "corpus_index.sha256")

	if !updateGolden {
		data, err := os.ReadFile(indexFile)
		if err != nil {
			t.Fatalf("reading corpus_index.sha256: %v\nRun with UPDATE_GOLDEN=1 to initialize.", err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			parts := strings.Fields(line)
			if len(parts) == 2 {
				indexMap[parts[1]] = parts[0]
			}
		}
	}

	for _, journalPath := range files {
		scenario := strings.TrimSuffix(filepath.Base(journalPath), ".jsonl")
		t.Run(scenario, func(t *testing.T) {
			got := runReplayInPTY(t, binPath, journalPath, nil)
			goldenFile := filepath.Join(corpusDir, scenario+".golden.txt")

			sum := sha256.Sum256([]byte(got))
			gotHash := hex.EncodeToString(sum[:])

			if updateGolden {
				if err := os.WriteFile(goldenFile, []byte(got+"\n"), 0644); err != nil {
					t.Fatalf("writing golden file %s: %v", goldenFile, err)
				}
				indexMap[scenario+".golden.txt"] = gotHash
				return
			}

			wantBytes, err := os.ReadFile(goldenFile)
			if err != nil {
				t.Fatalf("missing golden file %s: %v\nRegeneration requires review sign-off. Run with UPDATE_GOLDEN=1 to regenerate.", goldenFile, err)
			}
			want := strings.TrimRight(string(wantBytes), "\n")

			if got != want {
				t.Errorf("scenario %q output mismatch!\n--- GOT (hash: %s) ---\n%s\n--- WANT (expected: %s) ---\n%s\nRegeneration requires review sign-off. Run with UPDATE_GOLDEN=1 to regenerate.",
					scenario, gotHash, got, indexMap[scenario+".golden.txt"], want)
			}

			if expectedHash, ok := indexMap[scenario+".golden.txt"]; ok {
				if gotHash != expectedHash {
					t.Errorf("scenario %q sha256 mismatch: got %s, want %s in corpus_index.sha256", scenario, gotHash, expectedHash)
				}
			}
		})
	}

	if updateGolden {
		var indexLines []string
		for name, h := range indexMap {
			indexLines = append(indexLines, fmt.Sprintf("%s  %s", h, name))
		}
		sort.Strings(indexLines)
		if err := os.WriteFile(indexFile, []byte(strings.Join(indexLines, "\n")+"\n"), 0644); err != nil {
			t.Fatalf("writing corpus_index.sha256: %v", err)
		}
		t.Logf("Updated golden corpus and corpus_index.sha256 with %d scenarios.", len(indexLines))
	}
}

// TestCorpusPerturbationMatrix verifies that runner ambient environment variations
// do not affect replay fingerprints across any scenario.
func TestCorpusPerturbationMatrix(t *testing.T) {
	binPath := getOrBuildCorpusBinary(t)
	corpusDir := "../../testdata/replay-corpus"

	files, err := filepath.Glob(filepath.Join(corpusDir, "*.jsonl"))
	if err != nil {
		t.Fatalf("globbing corpus files: %v", err)
	}
	sort.Strings(files)

	// Baseline run
	baseHashes := make(map[string]string)
	for _, f := range files {
		out := runReplayInPTY(t, binPath, f, nil)
		h := sha256.Sum256([]byte(out))
		baseHashes[filepath.Base(f)] = hex.EncodeToString(h[:])
	}

	tempDir1 := t.TempDir()
	tempDir2 := t.TempDir()

	axes := []struct {
		name string
		env  map[string]string
	}{
		{name: "TERM=dumb", env: map[string]string{"TERM": "dumb"}},
		{name: "NO_COLOR=1", env: map[string]string{"NO_COLOR": "1"}},
		{name: "COLUMNS=17_LINES=3", env: map[string]string{"COLUMNS": "17", "LINES": "3"}},
		{name: "TZ=Asia/Baghdad", env: map[string]string{"TZ": "Asia/Baghdad"}},
		{name: "LANG=ar_IQ.UTF-8", env: map[string]string{"LANG": "ar_IQ.UTF-8", "LC_ALL": "ar_IQ.UTF-8"}},
		{name: "HOME_tempdir1", env: map[string]string{"HOME": tempDir1}},
		{name: "HOME_tempdir2", env: map[string]string{"HOME": tempDir2}},
		{name: "NABD_CONFIG_override", env: map[string]string{"NABD_CONFIG": filepath.Join(tempDir1, "conf.json")}},
	}

	for _, axis := range axes {
		t.Run(axis.name, func(t *testing.T) {
			for _, f := range files {
				name := filepath.Base(f)
				gotOut := runReplayInPTY(t, binPath, f, axis.env)
				h := sha256.Sum256([]byte(gotOut))
				gotHash := hex.EncodeToString(h[:])
				if gotHash != baseHashes[name] {
					t.Fatalf("axis %s changed fingerprint of %s: got %s, want baseline %s", axis.name, name, gotHash, baseHashes[name])
				}
			}
		})
	}
}
