package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestRegistry(t *testing.T) (string, *Registry) {
	t.Helper()
	root := t.TempDir()
	reg, err := NewRegistry(root, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
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
	return reg.RunApproved(context.Background(), name, raw)
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
		"AUDIT_SECRET=" + os.Getenv("AUDIT_SECRET") + "\n" +
		"GOPROXY=" + os.Getenv("GOPROXY") + "\n"
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
	for _, want := range []string{"GOMEMLIMIT=512MiB", "GOGC=50", "GOFLAGS=-mod=readonly"} {
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
	if !strings.Contains(out, "truncated") || !strings.Contains(out, "intentional failure") {
		t.Fatalf("capped output lost its head or tail: %d bytes", len(out))
	}
}
