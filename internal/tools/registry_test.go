package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/amiraq1/nabd/internal/audit"
)

func newTestRegistry(t *testing.T) (string, *Registry) {
	t.Helper()
	root := t.TempDir()
	reg, err := NewRegistry(root, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	return root, reg
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runTool(t *testing.T, reg *Registry, name string, args map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return reg.Run(context.Background(), name, raw)
}

func runToolApproved(t *testing.T, reg *Registry, name string, args map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := reg.Approve(name, raw)
	if err != nil {
		t.Fatalf("Approve(%s): %v", name, err)
	}
	return reg.RunApproved(context.Background(), name, raw, approval)
}

func listedNames(t *testing.T, out string) []string {
	t.Helper()
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			names = append(names, fields[1])
		}
	}
	return names
}

func TestJailRejectsEscapes(t *testing.T) {
	root, reg := newTestRegistry(t)
	parent := filepath.Dir(root)
	canary := filepath.Join(parent, "canary.txt")
	writeTestFile(t, canary, "CANARY-OUTSIDE")
	writeTestFile(t, filepath.Join(parent, "ws-evil", "secret.txt"), "SIBLING")
	writeTestFile(t, filepath.Join(root, "inside.txt"), "INSIDE")

	cases := []struct {
		name string
		path string
	}{
		{"dotdot", "../canary.txt"},
		{"dotdot-nested", "sub/../../canary.txt"},
		{"absolute", canary},
		{"sibling-prefix", "../ws-evil/secret.txt"},
		{"dotdot-dir", ".."},
		{"nul-byte", "inside\x00.txt"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, tool := range []string{"read_file", "list_dir"} {
				out := runTool(t, reg, tool, map[string]any{"path": tc.path})
				if !strings.HasPrefix(out, "Error") {
					t.Errorf("%s accepted escape %q: %.120q", tool, tc.path, out)
				}
			}
		})
	}

	if out := runTool(t, reg, "read_file", map[string]any{"path": "inside.txt"}); out != "INSIDE" {
		t.Fatalf("read_file inside jail failed: %q", out)
	}
	if out := runTool(t, reg, "list_dir", map[string]any{"path": "."}); !strings.Contains(out, "inside.txt") {
		t.Fatalf("list_dir inside jail failed: %q", out)
	}
}

func TestJailRejectsSymlinkEscape(t *testing.T) {
	root, reg := newTestRegistry(t)
	outside := filepath.Join(filepath.Dir(root), "outside")
	writeTestFile(t, filepath.Join(outside, "secret.txt"), "SECRET")
	if err := os.Symlink(outside, filepath.Join(root, "linkdir")); err != nil {
		t.Skipf("symlink creation unsupported: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "linkfile")); err != nil {
		t.Skipf("symlink creation unsupported: %v", err)
	}

	for _, path := range []string{"linkdir/secret.txt", "linkfile"} {
		if out := runTool(t, reg, "read_file", map[string]any{"path": path}); !strings.HasPrefix(out, "Error") {
			t.Errorf("read_file followed escaping symlink %q: %.120q", path, out)
		}
	}
	if out := runTool(t, reg, "list_dir", map[string]any{"path": "linkdir"}); !strings.HasPrefix(out, "Error") {
		t.Errorf("list_dir followed escaping symlink: %.120q", out)
	}
}

func TestSensitivePathsDenied(t *testing.T) {
	root, reg := newTestRegistry(t)
	denied := []string{
		".nabd/backup.txt",
		".git/config",
		".env",
		".env.local",
		"id_ed25519",
		"server.pem",
		"secret.key",
	}
	allowed := []string{
		".env.example",
		".gitignore",
		"id_ed25519.pub",
		"main.go",
	}
	for _, name := range denied {
		writeTestFile(t, filepath.Join(root, name), "SECRET")
	}
	for _, name := range allowed {
		writeTestFile(t, filepath.Join(root, name), "PUBLIC")
	}

	for _, name := range denied {
		out := runTool(t, reg, "read_file", map[string]any{"path": name})
		if !strings.Contains(out, "sensitive path denied") {
			t.Errorf("read_file(%q) = %q, want sensitive-path denial", name, out)
		}
		if _, err := reg.FileDigest(name); err == nil {
			t.Errorf("FileDigest(%q) succeeded, want denial", name)
		}
		out = runToolApproved(t, reg, "edit_file", map[string]any{
			"path": name, "old_text": "SECRET", "new_text": "CHANGED",
		})
		if !strings.Contains(out, "sensitive path denied") {
			t.Errorf("edit_file(%q) = %q, want sensitive-path denial", name, out)
		}
	}

	for _, name := range allowed {
		if out := runTool(t, reg, "read_file", map[string]any{"path": name}); out != "PUBLIC" {
			t.Errorf("read_file(%q) = %q, want PUBLIC", name, out)
		}
	}

	out := runTool(t, reg, "list_dir", map[string]any{"path": "."})
	names := listedNames(t, out)
	for _, name := range denied {
		base := filepath.Base(name)
		for _, listed := range names {
			if listed == base {
				t.Errorf("list_dir exposed sensitive entry %q", base)
			}
		}
	}
	for _, name := range allowed {
		found := false
		for _, listed := range names {
			if listed == name {
				found = true
			}
		}
		if !found {
			t.Errorf("list_dir hid allowed entry %q", name)
		}
	}
	if out := runTool(t, reg, "list_dir", map[string]any{"path": ".nabd"}); !strings.Contains(out, "sensitive path denied") {
		t.Errorf("list_dir(.nabd) = %q, want denial", out)
	}
}

func TestRunFailsClosedWithoutApprover(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "target.txt"), "original")

	cases := []struct {
		tool string
		args map[string]any
	}{
		{"edit_file", map[string]any{"path": "target.txt", "old_text": "original", "new_text": "changed"}},
		{"go_verify", map[string]any{"pkg": "./..."}},
		{"mcp_call", map[string]any{"command": "/bin/sh", "tool": "anything"}},
	}
	for _, tc := range cases {
		out := runTool(t, reg, tc.tool, tc.args)
		if !strings.Contains(out, "requires approval") {
			t.Errorf("Run(%s) = %q, want approval refusal", tc.tool, out)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(root, "target.txt")); string(got) != "original" {
		t.Fatalf("refused edit modified the file: %q", got)
	}

	if out := runTool(t, reg, "read_file", map[string]any{"path": "target.txt"}); out != "original" {
		t.Errorf("read-only tool was blocked: %q", out)
	}
	if out := runTool(t, reg, "list_dir", map[string]any{"path": "."}); !strings.Contains(out, "target.txt") {
		t.Errorf("read-only list_dir was blocked: %q", out)
	}
	if out := runTool(t, reg, "unknown_tool", map[string]any{}); !strings.Contains(out, "unknown tool") {
		t.Errorf("unknown tool = %q, want unknown-tool error", out)
	}
}

func TestRunApprovedAndApproverDecisions(t *testing.T) {
	t.Run("RunApproved executes", func(t *testing.T) {
		root, reg := newTestRegistry(t)
		writeTestFile(t, filepath.Join(root, "target.txt"), "original")
		out := runToolApproved(t, reg, "edit_file", map[string]any{
			"path": "target.txt", "old_text": "original", "new_text": "changed",
		})
		if !strings.Contains(out, "Successfully") {
			t.Fatalf("RunApproved edit = %q", out)
		}
	})

	t.Run("approver denies", func(t *testing.T) {
		root := t.TempDir()
		called := 0
		reg, err := NewRegistry(root, func(tool, summary string) bool {
			called++
			if summary == "" {
				t.Error("approver received an empty summary")
			}
			return false
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reg.Close() })
		writeTestFile(t, filepath.Join(root, "target.txt"), "original")
		out := runTool(t, reg, "edit_file", map[string]any{
			"path": "target.txt", "old_text": "original", "new_text": "changed",
		})
		if out != "Action rejected by user." {
			t.Fatalf("denial = %q", out)
		}
		if called != 1 {
			t.Fatalf("approver calls = %d, want 1", called)
		}
		if got, _ := os.ReadFile(filepath.Join(root, "target.txt")); string(got) != "original" {
			t.Fatalf("denied edit wrote the file: %q", got)
		}
	})

	t.Run("approver grants", func(t *testing.T) {
		root := t.TempDir()
		reg, err := NewRegistry(root, func(string, string) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reg.Close() })
		writeTestFile(t, filepath.Join(root, "target.txt"), "original")
		out := runTool(t, reg, "edit_file", map[string]any{
			"path": "target.txt", "old_text": "original", "new_text": "changed",
		})
		if !strings.Contains(out, "Successfully") {
			t.Fatalf("granted edit = %q", out)
		}
	})
}

func TestRequiresApprovalDefaultsClosed(t *testing.T) {
	_, reg := newTestRegistry(t)
	reg.handlers["future_writer"] = ToolSpec{Name: "future_writer"}
	if !reg.RequiresApproval("future_writer") {
		t.Fatal("zero-value ToolSpec did not require approval")
	}
	reg.handlers["future_reader"] = ToolSpec{Name: "future_reader", ReadOnly: true}
	if reg.RequiresApproval("future_reader") {
		t.Fatal("read-only tool required approval")
	}
	if !reg.RequiresApproval("missing") {
		t.Fatal("unknown tool did not require approval")
	}
}

func TestDisableRemovesTool(t *testing.T) {
	_, reg := newTestRegistry(t)
	if !reg.Has("mcp_call") {
		t.Fatal("mcp_call not registered by default")
	}
	reg.Disable("mcp_call")
	if reg.Has("mcp_call") {
		t.Fatal("mcp_call still registered after Disable")
	}
	if out := runTool(t, reg, "mcp_call", map[string]any{"command": "x", "tool": "y"}); !strings.Contains(out, "unknown tool") {
		t.Errorf("disabled tool = %q, want unknown-tool error", out)
	}
	for _, def := range reg.OpenAITools() {
		if fn, ok := def["function"].(map[string]any); ok && fn["name"] == "mcp_call" {
			t.Fatal("disabled tool is still advertised")
		}
	}
}

func TestEditMatchRules(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "dup.txt"), "alpha\nbeta\nalpha\n")

	if out := runToolApproved(t, reg, "edit_file", map[string]any{
		"path": "dup.txt", "old_text": "alpha", "new_text": "gamma",
	}); !strings.Contains(out, "2 times") {
		t.Errorf("multiple matches = %q", out)
	}
	if out := runToolApproved(t, reg, "edit_file", map[string]any{
		"path": "dup.txt", "old_text": "missing", "new_text": "gamma",
	}); !strings.Contains(out, "not found") {
		t.Errorf("zero matches = %q", out)
	}
	if out := runToolApproved(t, reg, "edit_file", map[string]any{
		"path": "dup.txt", "old_text": "", "new_text": "gamma",
	}); !strings.Contains(out, "empty") {
		t.Errorf("empty old_text = %q", out)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "dup.txt")); string(got) != "alpha\nbeta\nalpha\n" {
		t.Fatalf("rejected edits modified the file: %q", got)
	}
}

func TestFileDigest(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "a.txt"), "hello")
	got, err := reg.FileDigest("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("hello"))
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("digest = %q, want %q", got, want)
	}
	writeTestFile(t, filepath.Join(root, "a.txt"), "changed")
	got2, err := reg.FileDigest("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got2 == got {
		t.Fatal("digest did not change with content")
	}
}

func TestGoVerifyEnvAllowlist(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "go.mod"), "module auditenv\n\ngo 1.22\n")
	writeTestFile(t, filepath.Join(root, "env_test.go"), `package auditenv

import (
	"os"
	"testing"
)

func TestEnv(t *testing.T) {
	env := "GOMEMLIMIT=" + os.Getenv("GOMEMLIMIT") + "\n" +
		"GOGC=" + os.Getenv("GOGC") + "\n" +
		"GOFLAGS=" + os.Getenv("GOFLAGS") + "\n" +
		"GOPROXY=" + os.Getenv("GOPROXY") + "\n" +
		"GOTOOLCHAIN=" + os.Getenv("GOTOOLCHAIN") + "\n" +
		"AUDIT_SECRET=" + os.Getenv("AUDIT_SECRET") + "\n"
	if err := os.WriteFile("env-dump.txt", []byte(env), 0o644); err != nil {
		t.Fatal(err)
	}
}
`)

	t.Setenv("AUDIT_SECRET", "sk-live-FAKE-SECRET")
	t.Setenv("GOPROXY", "https://user:secret@proxy.invalid")

	out := runToolApproved(t, reg, "go_verify", map[string]any{"pkg": "./..."})
	dump, err := os.ReadFile(filepath.Join(root, "env-dump.txt"))
	if err != nil {
		t.Fatalf("test binary did not run: %v\noutput:\n%s", err, out)
	}

	env := string(dump)
	for _, want := range []string{"GOMEMLIMIT=512MiB", "GOGC=50", "GOFLAGS=-mod=readonly", "GOPROXY=off", "GOTOOLCHAIN=local"} {
		if !strings.Contains(env, want) {
			t.Errorf("environment missing %q:\n%s", want, env)
		}
	}
	for _, secret := range []string{"sk-live-FAKE-SECRET", "user:secret", "GOPROXY=https"} {
		if strings.Contains(env, secret) {
			t.Errorf("environment leaked %q:\n%s", secret, env)
		}
	}
}

func TestGoVerifyOutputCap(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "go.mod"), "module auditloud\n\ngo 1.22\n")
	writeTestFile(t, filepath.Join(root, "loud_test.go"), `package auditloud

import (
	"fmt"
	"testing"
)

func TestLoud(t *testing.T) {
	for i := 0; i < 2000; i++ {
		fmt.Println("0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF")
	}
	t.Fatal("intentional failure")
}
`)

	out := runToolApproved(t, reg, "go_verify", map[string]any{"pkg": "./..."})
	if len(out) > maxOutputBytes+128 {
		t.Fatalf("output not capped: %d bytes", len(out))
	}
	if !strings.Contains(out, "FAIL") || !strings.Contains(out, "intentional failure") {
		t.Fatalf("summary lost the failure: %d bytes\n%s", len(out), out)
	}
	repeated := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "0123456789ABCDEF") {
			repeated++
		}
	}
	if repeated > maxSummaryTail {
		t.Fatalf("raw output was not summarized: %d repeated lines", repeated)
	}
}

func TestEditAtomicPreservesModeAndLeavesNoTemp(t *testing.T) {
	root, reg := newTestRegistry(t)
	path := filepath.Join(root, "script.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := runToolApproved(t, reg, "edit_file", map[string]any{
		"path": "script.sh", "old_text": "echo old", "new_text": "echo new",
	})
	if !strings.Contains(out, "Successfully") {
		t.Fatalf("edit = %q", out)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755", info.Mode().Perm())
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".nabd-tmp-") {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".nabd", "shadow")); err != nil {
		t.Fatalf("shadow directory missing: %v", err)
	}
}

func TestEditRejectsNoOp(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "a.txt"), "alpha\n")

	if out := runToolApproved(t, reg, "edit_file", map[string]any{
		"path": "a.txt", "old_text": "alpha", "new_text": "alpha",
	}); !strings.Contains(out, "identical") {
		t.Errorf("exact no-op = %q", out)
	}
	if out := runToolApproved(t, reg, "edit_file", map[string]any{
		"path": "a.txt", "old_text": "alpha\n", "new_text": "alpha\r\n",
	}); !strings.Contains(out, "line-ending normalization") {
		t.Errorf("CRLF no-op = %q", out)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(got) != "alpha\n" {
		t.Fatalf("no-op changed the file: %q", got)
	}
}

func TestEditPreservesCRLFInGo(t *testing.T) {
	root, reg := newTestRegistry(t)
	path := filepath.Join(root, "crlf.go")
	if err := os.WriteFile(path, []byte("package demo\r\n\r\nvar x = 1\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := runToolApproved(t, reg, "edit_file", map[string]any{
		"path": "crlf.go", "old_text": "var x = 1", "new_text": "var x = 2",
	})
	if !strings.Contains(out, "Successfully") {
		t.Fatalf("edit = %q", out)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "var x = 2\r\n") {
		t.Fatalf("CRLF not preserved:\n%q", got)
	}
	if strings.Contains(strings.ReplaceAll(string(got), "\r\n", ""), "\n") {
		t.Fatalf("mixed line endings after edit:\n%q", got)
	}
}

func TestEditRejectsLargeFile(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "big.txt"), strings.Repeat("A", maxEditBytes+1))

	out := runToolApproved(t, reg, "edit_file", map[string]any{
		"path": "big.txt", "old_text": "A", "new_text": "B",
	})
	if !strings.Contains(out, "too large") {
		t.Fatalf("large edit = %q", out)
	}
}

func TestReadFileCap(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "big.txt"), strings.Repeat("A", maxReadBytes+100))

	out, err := reg.readFile("big.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > maxReadBytes+64 {
		t.Fatalf("read returned %d bytes", len(out))
	}
	if !strings.Contains(out, "truncated at") {
		t.Fatal("truncation notice missing")
	}
}

func TestListDirCap(t *testing.T) {
	root, reg := newTestRegistry(t)
	for i := 0; i < maxListEntries+5; i++ {
		writeTestFile(t, filepath.Join(root, fmt.Sprintf("f%04d.txt", i)), "x")
	}

	out, err := reg.listDir(".")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != maxListEntries+1 {
		t.Fatalf("listed %d lines, want %d", len(lines), maxListEntries+1)
	}
	if !strings.Contains(out, "5 more entries") {
		t.Fatalf("entry-cap notice missing")
	}
	if !strings.HasPrefix(lines[0], "F  f0000.txt") {
		t.Fatalf("list is not sorted: %q", lines[0])
	}
}

func TestEditRejectsSymlink(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "real.txt"), "original")
	if err := os.Symlink("real.txt", filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlink creation unsupported: %v", err)
	}

	out := runToolApproved(t, reg, "edit_file", map[string]any{
		"path": "link.txt", "old_text": "original", "new_text": "changed",
	})
	if !strings.Contains(out, "symlinks is not supported") {
		t.Fatalf("symlink edit = %q", out)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "real.txt")); string(got) != "original" {
		t.Fatalf("symlink target was modified: %q", got)
	}
}

func TestEditThroughSymlinkedParentRejected(t *testing.T) {
	root, reg := newTestRegistry(t)
	outside := filepath.Join(filepath.Dir(root), "outside")
	writeTestFile(t, filepath.Join(outside, "secret.txt"), "outside")
	if err := os.Symlink(outside, filepath.Join(root, "linkdir")); err != nil {
		t.Skipf("symlink creation unsupported: %v", err)
	}

	out := runToolApproved(t, reg, "edit_file", map[string]any{
		"path": "linkdir/secret.txt", "old_text": "outside", "new_text": "changed",
	})
	if !strings.HasPrefix(out, "Error") {
		t.Fatalf("edit through symlinked parent = %q", out)
	}
	if got, _ := os.ReadFile(filepath.Join(outside, "secret.txt")); string(got) != "outside" {
		t.Fatalf("outside file was modified: %q", got)
	}
}

func TestUndoRestoresFile(t *testing.T) {
	root, reg := newTestRegistry(t)
	path := filepath.Join(root, "target.txt")
	writeTestFile(t, path, "original\n")

	if out := runToolApproved(t, reg, "edit_file", map[string]any{
		"path": "target.txt", "old_text": "original", "new_text": "changed",
	}); !strings.Contains(out, "Successfully") {
		t.Fatalf("edit = %q", out)
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "changed") {
		t.Fatalf("edit did not apply: %q", got)
	}

	msg, err := reg.Undo("target.txt")
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if !strings.Contains(msg, "Restored target.txt") {
		t.Fatalf("undo message = %q", msg)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "original\n" {
		t.Fatalf("undo content = %q", got)
	}
	if _, err := reg.Undo("target.txt"); err == nil {
		t.Fatal("second undo succeeded, want no-entry error")
	}
}

func TestUndoRefusesAfterDrift(t *testing.T) {
	root, reg := newTestRegistry(t)
	path := filepath.Join(root, "target.txt")
	writeTestFile(t, path, "original\n")

	runToolApproved(t, reg, "edit_file", map[string]any{
		"path": "target.txt", "old_text": "original", "new_text": "changed",
	})
	writeTestFile(t, path, "drifted\n")

	if _, err := reg.Undo("target.txt"); err == nil || !strings.Contains(err.Error(), "changed since the edit") {
		t.Fatalf("Undo after drift = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "drifted\n" {
		t.Fatalf("drift was overwritten: %q", got)
	}
}

func TestUndoNoArgRestoresLatest(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "a.txt"), "original-a\n")
	writeTestFile(t, filepath.Join(root, "b.txt"), "original-b\n")

	runToolApproved(t, reg, "edit_file", map[string]any{
		"path": "a.txt", "old_text": "original-a", "new_text": "changed-a",
	})
	time.Sleep(2 * time.Millisecond)
	runToolApproved(t, reg, "edit_file", map[string]any{
		"path": "b.txt", "old_text": "original-b", "new_text": "changed-b",
	})

	if _, err := reg.Undo(""); err != nil {
		t.Fatalf("Undo(any): %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "b.txt")); string(got) != "original-b\n" {
		t.Fatalf("latest file not restored: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a.txt")); !strings.Contains(string(got), "changed-a") {
		t.Fatalf("older edit was undone too: %q", got)
	}
}

func TestShadowDeniedToFileTools(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "target.txt"), "original\n")
	runToolApproved(t, reg, "edit_file", map[string]any{
		"path": "target.txt", "old_text": "original", "new_text": "changed",
	})

	entries, err := os.ReadDir(filepath.Join(root, ".nabd", "shadow"))
	if err != nil {
		t.Fatal(err)
	}
	var backup string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".bak") {
			backup = entry.Name()
		}
	}
	if backup == "" {
		t.Fatal("no shadow backup created")
	}

	if out := runTool(t, reg, "read_file", map[string]any{"path": ".nabd/shadow/" + backup}); !strings.Contains(out, "sensitive path denied") {
		t.Fatalf("shadow read = %q", out)
	}
	if out := runTool(t, reg, "list_dir", map[string]any{"path": ".nabd"}); !strings.Contains(out, "sensitive path denied") {
		t.Fatalf("shadow list = %q", out)
	}
}

func readAuditLog(t *testing.T, root string) ([]audit.Event, string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".nabd", "audit.log"))
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	var events []audit.Event
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var event audit.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid audit line %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events, string(data)
}

func TestAuditLogApprovalFlow(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "target.txt"), "original\n")
	raw, err := json.Marshal(map[string]any{
		"path": "target.txt", "old_text": "original", "new_text": "changed",
	})
	if err != nil {
		t.Fatal(err)
	}

	approval, err := reg.Approve("edit_file", raw)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if out := reg.RunApproved(context.Background(), "edit_file", raw, approval); !strings.Contains(out, "Successfully") {
		t.Fatalf("RunApproved = %q", out)
	}

	events, rawLog := readAuditLog(t, root)
	if len(events) != 3 {
		t.Fatalf("audit events = %d, want 3: %+v", len(events), events)
	}
	want := []string{"requested", "approved", "applied"}
	for i, event := range events {
		if event.Event != want[i] {
			t.Errorf("event %d = %q, want %q", i, event.Event, want[i])
		}
		if event.Tool != "edit_file" || event.ArgsSHA256 == "" {
			t.Errorf("event %d missing tool/args hash: %+v", i, event)
		}
	}
	if events[0].Detail != "target.txt" {
		t.Errorf("requested detail = %q", events[0].Detail)
	}
	if events[2].ResultSHA256 == "" {
		t.Errorf("applied event has no result hash")
	}
	for _, secret := range []string{"original", "changed", "Successfully"} {
		if strings.Contains(rawLog, secret) {
			t.Errorf("audit log leaks content %q:\n%s", secret, rawLog)
		}
	}
}

func TestAuditLogDeniedWithoutApprover(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "target.txt"), "original\n")

	out := runTool(t, reg, "edit_file", map[string]any{
		"path": "target.txt", "old_text": "original", "new_text": "changed",
	})
	if !strings.Contains(out, "requires approval") {
		t.Fatalf("Run = %q", out)
	}

	events, _ := readAuditLog(t, root)
	if len(events) != 2 || events[0].Event != "requested" || events[1].Event != "denied" {
		t.Fatalf("audit events = %+v", events)
	}
	if !strings.Contains(events[1].Reason, "no approver") {
		t.Fatalf("denied reason = %q", events[1].Reason)
	}
}

func TestAuditLogFailClosedForWrites(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "target.txt"), "original\n")
	if err := os.WriteFile(filepath.Join(root, ".nabd"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(map[string]any{
		"path": "target.txt", "old_text": "original", "new_text": "changed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Approve("edit_file", raw); err == nil {
		t.Fatal("Approve succeeded without a writable audit log")
	}
	if out := runTool(t, reg, "edit_file", map[string]any{
		"path": "target.txt", "old_text": "original", "new_text": "changed",
	}); !strings.HasPrefix(out, "Error") {
		t.Fatalf("Run = %q", out)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "target.txt")); string(got) != "original\n" {
		t.Fatalf("write happened without audit: %q", got)
	}
}

func TestReadOnlyToolsSurviveAuditFailure(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "a.txt"), "hello")
	if err := os.WriteFile(filepath.Join(root, ".nabd"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	if out := runTool(t, reg, "read_file", map[string]any{"path": "a.txt"}); out != "hello" {
		t.Fatalf("read_file = %q", out)
	}
}

func TestApprovalTokenRequiredAndSingleUse(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "target.txt"), "original\n")
	raw, err := json.Marshal(map[string]any{
		"path": "target.txt", "old_text": "original", "new_text": "changed",
	})
	if err != nil {
		t.Fatal(err)
	}

	if out := reg.RunApproved(context.Background(), "edit_file", raw, Approval{}); !strings.Contains(out, "invalid or expired") {
		t.Fatalf("RunApproved without token = %q", out)
	}

	approval, err := reg.Approve("edit_file", raw)
	if err != nil {
		t.Fatal(err)
	}
	other, err := json.Marshal(map[string]any{
		"path": "target.txt", "old_text": "original", "new_text": "different",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out := reg.RunApproved(context.Background(), "edit_file", other, approval); !strings.Contains(out, "invalid or expired") {
		t.Fatalf("RunApproved with wrong args = %q", out)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "target.txt")); string(got) != "original\n" {
		t.Fatalf("wrong-args approval wrote the file: %q", got)
	}

	if out := reg.RunApproved(context.Background(), "edit_file", raw, approval); !strings.Contains(out, "Successfully") {
		t.Fatalf("RunApproved with token = %q", out)
	}
	if out := reg.RunApproved(context.Background(), "edit_file", raw, approval); !strings.Contains(out, "invalid or expired") {
		t.Fatalf("token reuse = %q", out)
	}
}

func TestApprovalTokenExpires(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "target.txt"), "original\n")
	raw, err := json.Marshal(map[string]any{
		"path": "target.txt", "old_text": "original", "new_text": "changed",
	})
	if err != nil {
		t.Fatal(err)
	}

	base := time.Now()
	reg.now = func() time.Time { return base }
	approval, err := reg.Approve("edit_file", raw)
	if err != nil {
		t.Fatal(err)
	}
	reg.now = func() time.Time { return base.Add(approvalTTL + time.Second) }
	if out := reg.RunApproved(context.Background(), "edit_file", raw, approval); !strings.Contains(out, "invalid or expired") {
		t.Fatalf("expired token = %q", out)
	}
}

func TestApproveRejectsReadOnlyTool(t *testing.T) {
	_, reg := newTestRegistry(t)
	if _, err := reg.Approve("read_file", json.RawMessage(`{"path":"x"}`)); err == nil {
		t.Fatal("Approve accepted a read-only tool")
	}
}

func TestUndoIsAudited(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "target.txt"), "original\n")
	runToolApproved(t, reg, "edit_file", map[string]any{
		"path": "target.txt", "old_text": "original", "new_text": "changed",
	})

	if _, err := reg.Undo("target.txt"); err != nil {
		t.Fatalf("Undo: %v", err)
	}

	events, _ := readAuditLog(t, root)
	var undoEvents []audit.Event
	for _, event := range events {
		if event.Tool == "undo" {
			undoEvents = append(undoEvents, event)
		}
	}
	if len(undoEvents) != 2 {
		t.Fatalf("undo audit events = %+v", undoEvents)
	}
	if undoEvents[0].Event != "requested" || undoEvents[1].Event != "applied" {
		t.Fatalf("undo events = %+v", undoEvents)
	}
	if undoEvents[0].ArgsSHA256 == "" || undoEvents[1].ResultSHA256 == "" {
		t.Fatalf("undo hashes missing: %+v", undoEvents)
	}
	if undoEvents[0].Detail != "target.txt" {
		t.Fatalf("undo detail = %q", undoEvents[0].Detail)
	}
}
