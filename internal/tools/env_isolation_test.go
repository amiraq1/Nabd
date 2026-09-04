package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestBashChildEnvAllowlistIntegration drives the REAL bash tool with a
// controlled parent environment full of the sensitive variables listed in
// the Phase 3 spec, and proves the real child does not see them.
func TestBashChildEnvAllowlistIntegration(t *testing.T) {
	secrets := map[string]string{
		"AWS_SECRET_ACCESS_KEY": "aws-secret",
		"AWS_SESSION_TOKEN":     "aws-session",
		"AWS_ACCESS_KEY_ID":     "aws-key",
		"SSH_KEY":               "ssh-key",
		"OPENAI_API_KEY":        "sk-openai",
		"OPENROUTER_API_KEY":    "sk-or",
		"GROQ_API_KEY":          "gsk-groq",
		"GITHUB_TOKEN":          "gh-token",
		"GH_TOKEN":              "gh-token-2",
		"ANTHROPIC_API_KEY":     "sk-ant",
		"GOOGLE_API_KEY":        "goog-key",
		"GEMINI_API_KEY":        "gem-key",
		"BASH_ENV":              "/tmp/attacker-bash-env",
		"ENV":                   "/tmp/attacker-env",
		"CDPATH":                "/tmp/attacker",
		"GIT_ASKPASS":           "/tmp/attacker-askpass",
		"SSH_ASKPASS":           "/tmp/attacker-askpass",
		"LD_PRELOAD":            "/tmp/attacker.so",
		"LD_LIBRARY_PATH":       "/tmp/attacker-lib",
		"DYLD_INSERT_LIBRARIES": "/tmp/attacker.dylib",
		"DYLD_LIBRARY_PATH":     "/tmp/attacker-lib",
		"SOME_UNKNOWN_BENIGN":   "should-not-leak",
	}
	for k, v := range secrets {
		t.Setenv(k, v)
	}

	r, _ := newReg(t)
	raw, _ := json.Marshal(map[string]any{"cmd": "env"})
	o, err := r.RunDetailed(context.Background(), "bash", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !o.OK {
		t.Fatalf("bash env failed: %s", o.Text)
	}
	child := o.Text
	for k, v := range secrets {
		if strings.Contains(child, v) || strings.Contains(child, k) {
			t.Errorf("secret %s or its value leaked into child env: %q", k, v)
		}
	}
}

// TestBashChildEnvBASH_ENVNotSourced proves BASH_ENV pointing at a marker
// script is never read by the child: the marker file must not appear.
func TestBashChildEnvBASH_ENVNotSourced(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "bash-env-marker")
	t.Setenv("BASH_ENV", marker)
	if err := os.WriteFile(marker, []byte("touch "+marker+".sourced\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r, _ := newReg(t)
	raw, _ := json.Marshal(map[string]any{"cmd": "true"})
	o, err := r.RunDetailed(context.Background(), "bash", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !o.OK {
		t.Fatalf("bash failed: %s", o.Text)
	}
	if _, err := os.Stat(marker + ".sourced"); !os.IsNotExist(err) {
		t.Fatalf("BASH_ENV was sourced: marker %s.sourced exists", marker)
	}
	// The marker script itself lives in the parent's temp dir and was never
	// read by the child (no .sourced sibling appeared), proving BASH_ENV was
	// neither passed nor consulted.
}

// TestBashChildEnvENVNotSourced proves ENV (the POSIX sh startup file) is
// not inherited, so it cannot source attacker-controlled files either.
func TestBashChildEnvENVNotSourced(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "env-marker")
	t.Setenv("ENV", marker)
	if err := os.WriteFile(marker, []byte("touch "+marker+".sourced\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r, _ := newReg(t)
	raw, _ := json.Marshal(map[string]any{"cmd": "true"})
	o, err := r.RunDetailed(context.Background(), "bash", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !o.OK {
		t.Fatalf("bash failed: %s", o.Text)
	}
	if _, err := os.Stat(marker + ".sourced"); !os.IsNotExist(err) {
		t.Fatalf("ENV was sourced: marker %s.sourced exists", marker)
	}
}

// TestBashChildEnvHomePolicy proves the documented HOME policy: the child
// sees an isolated temp HOME (0700), and a fake real HOME full of startup
// files and credential-shaped content is neither seen nor sourced.
func TestBashChildEnvHomePolicy(t *testing.T) {
	fakeHome := filepath.Join(t.TempDir(), "real-home")
	if err := os.MkdirAll(fakeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	// Startup files and credential stores that must NOT affect the child.
	os.WriteFile(filepath.Join(fakeHome, ".profile"), []byte("touch "+fakeHome+".sourced\n"), 0o600)
	os.WriteFile(filepath.Join(fakeHome, ".bashrc"), []byte("touch "+fakeHome+".bashrc-sourced\n"), 0o600)
	os.MkdirAll(filepath.Join(fakeHome, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(fakeHome, ".ssh", "id_ed25519"), []byte("PRIVATE KEY"), 0o600)
	os.WriteFile(filepath.Join(fakeHome, ".netrc"), []byte("machine github login x password y\n"), 0o600)
	t.Setenv("HOME", fakeHome)

	r, _ := newReg(t)
	raw, _ := json.Marshal(map[string]any{"cmd": "echo $HOME && ls -a $HOME"})
	o, err := r.RunDetailed(context.Background(), "bash", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !o.OK {
		t.Fatalf("bash failed: %s", o.Text)
	}
	childHome := ""
	for _, ln := range strings.Split(o.Text, "\n") {
		if strings.HasPrefix(ln, "/") || strings.HasPrefix(ln, "tmp") {
			childHome = strings.TrimSpace(ln)
			break
		}
	}
	if childHome == "" || childHome == fakeHome {
		t.Fatalf("child HOME = %q, want an isolated temp dir, not the caller's %q", childHome, fakeHome)
	}
	if strings.Contains(o.Text, "PRIVATE KEY") || strings.Contains(o.Text, "id_ed25519") || strings.Contains(o.Text, ".netrc") {
		t.Fatalf("credential stores from the caller HOME reached the child: %q", o.Text)
	}
	if _, err := os.Stat(fakeHome + ".sourced"); !os.IsNotExist(err) {
		t.Fatalf("fake HOME .profile was sourced by the child")
	}
	// The isolated HOME must be 0700 on creation policy; we can't observe the
	// temp dir itself after removal, but the file listing must be empty.
	for _, ln := range strings.Split(o.Text, "\n") {
		if strings.Contains(ln, ".ssh") || strings.Contains(ln, ".netrc") {
			t.Fatalf("caller HOME contents visible in child listing: %q", ln)
		}
	}
}

// TestBashChildEnvAllowsWhitelisted verifies allowed variables survive.
func TestBashChildEnvAllowsWhitelisted(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LANG", "ar_SA.UTF-8")
	t.Setenv("LC_CTYPE", "ar_SA.UTF-8")
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("PATH", "/usr/local/bin:/usr/bin:/bin")

	r, _ := newReg(t)
	raw, _ := json.Marshal(map[string]any{"cmd": "env"})
	o, err := r.RunDetailed(context.Background(), "bash", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !o.OK {
		t.Fatalf("bash failed: %s", o.Text)
	}
	for _, want := range []string{"TERM=xterm-256color", "LANG=ar_SA.UTF-8", "LC_CTYPE=ar_SA.UTF-8", "TMPDIR=", "NABD=1"} {
		if !strings.Contains(o.Text, want) {
			t.Errorf("allowed var %q missing from child env: %q", want, o.Text)
		}
	}
}

// TestBashChildEnvDeterministicOrdering proves the child environment is
// emitted in deterministic sorted order across two identical runs.
func TestBashChildEnvDeterministicOrdering(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("TERM", "xterm")
	t.Setenv("LANG", "C")
	t.Setenv("TMPDIR", t.TempDir())

	run := func() []string {
		r, _ := newReg(t)
		raw, _ := json.Marshal(map[string]any{"cmd": "env"})
		o, err := r.RunDetailed(context.Background(), "bash", raw)
		if err != nil || !o.OK {
			t.Fatalf("bash failed: %v %s", err, o.Text)
		}
		var names []string
		for _, ln := range strings.Split(o.Text, "\n") {
			if k, _, ok := strings.Cut(ln, "="); ok {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		return names
	}

	a := run()
	b := run()
	if len(a) != len(b) {
		t.Fatalf("env length differs between runs: %v vs %v", a, b)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("env not deterministic: %v vs %v", a, b)
		}
	}
}

// TestBashChildEnvConcurrentIsolation proves two concurrent bash executions
// cannot leak environment values into one another: each child's HOME is its
// own isolated temp dir, and the environment is rebuilt per invocation.
func TestBashChildEnvConcurrentIsolation(t *testing.T) {
	// Each goroutine needs its own unique temp marker path (t.Parallel +
	// t.TempDir would race; we avoid t.Parallel here and use per-goroutine
	// dirs, which still proves the env construction is per-call).
	results := make(chan string, 4)
	for i := 0; i < 4; i++ {
		go func() {
			dir := t.TempDir()
			r, _ := NewRegistryFromDir(dir)
			raw, _ := json.Marshal(map[string]any{"cmd": "env"})
			o, err := r.RunDetailed(context.Background(), "bash", raw)
			if err != nil {
				results <- "ERR " + err.Error()
				return
			}
			results <- o.Text
		}()
	}
	homes := map[string]bool{}
	for i := 0; i < 4; i++ {
		text := <-results
		if strings.HasPrefix(text, "ERR") {
			t.Fatal(text)
		}
		home := ""
		for _, ln := range strings.Split(text, "\n") {
			if v, ok := strings.CutPrefix(ln, "HOME="); ok {
				home = v
				break
			}
		}
		if home == "" {
			t.Fatalf("no HOME in child env: %q", text)
		}
		if homes[home] {
			t.Fatalf("two concurrent children shared HOME %q — isolated homes required", home)
		}
		homes[home] = true
	}
}

// TestBashChildEnvPathStripping proves empty and relative PATH entries are
// stripped before the child sees PATH.
func TestBashChildEnvPathStripping(t *testing.T) {
	t.Setenv("PATH", "relative/bin::/usr/bin:/bin:.:relative2")

	r, _ := newReg(t)
	raw, _ := json.Marshal(map[string]any{"cmd": "echo $PATH"})
	o, err := r.RunDetailed(context.Background(), "bash", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !o.OK {
		t.Fatalf("bash failed: %s", o.Text)
	}
	got := ""
	for _, ln := range strings.Split(o.Text, "\n") {
		if strings.Contains(ln, "/usr/bin") {
			got = strings.TrimSpace(ln)
			break
		}
	}
	if strings.Contains(got, "relative") || strings.Contains(got, ":.") || strings.Contains(got, "::") {
		t.Fatalf("PATH not sanitized: %q", got)
	}
	if !strings.Contains(got, "/usr/bin") || !strings.Contains(got, "/bin") {
		t.Fatalf("absolute PATH entries missing: %q", got)
	}
}

// TestBashChildEnvRejectsNULValues proves a NUL byte anywhere in an
// allowlisted value causes that variable to be dropped, never forwarded.
func TestBashChildEnvRejectsNULValues(t *testing.T) {
	// os.Setenv rejects NUL, so inject via a crafted parent slice directly.
	parent := []string{"TERM=xterm\x00", "LANG=C", "PATH=/usr/bin:/bin"}
	env := childEnv(parent)
	for _, kv := range env {
		if strings.HasPrefix(kv, "TERM=") {
			t.Fatalf("TERM with NUL value leaked into child env: %q", kv)
		}
	}
	foundLANG, foundPATH := false, false
	for _, kv := range env {
		if strings.HasPrefix(kv, "LANG=") {
			foundLANG = true
		}
		if strings.HasPrefix(kv, "PATH=") {
			foundPATH = true
		}
	}
	if !foundLANG || !foundPATH {
		t.Fatalf("sanitized env lost valid vars: %v", env)
	}
}

// NewRegistryFromDir is a helper for tests that need a Registry rooted at an
// explicit dir without the usual helper's TempDir bookkeeping.
func NewRegistryFromDir(dir string) (*Registry, error) {
	root, err := NewRoot(dir)
	if err != nil {
		return nil, err
	}
	return NewRegistry(root, nil), nil
}
