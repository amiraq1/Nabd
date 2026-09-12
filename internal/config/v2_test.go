package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestV2StrictRouterPreservesRouteOrder(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "g-secret")
	t.Setenv("NVIDIA_API_KEY", "n-secret")
	p := filepath.Join(t.TempDir(), "config.v2.json")
	body := `{"version":2,"provider":"router","router_mode":"fallback","routes":[{"provider":"groq","model":"openai/gpt-oss-120b"},{"provider":"nvidia","model":"deepseek-ai/deepseek-v4-pro-0813"}],"credentials":{"groq":{"source":"env"},"nvidia":{"source":"env"}}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ParseV2File(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "groq:openai/gpt-oss-120b,nvidia:deepseek-ai/deepseek-v4-pro-0813"
	if got["NABD_ROUTES"] != want {
		t.Fatalf("routes=%q want %q", got["NABD_ROUTES"], want)
	}
}

func TestV2RejectsUnknownAndCommandSource(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"unknown": `{"version":2,"provider":"groq","priority":1,"credentials":{"groq":{"source":"env"}}}`,
		"command": `{"version":2,"provider":"groq","credentials":{"groq":{"source":"command"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, name)
			if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ParseV2File(p); err == nil {
				t.Fatal("invalid v2 accepted")
			}
		})
	}
}

func TestV1AndV2TogetherAreFatal(t *testing.T) {
	dir := t.TempDir()
	v1 := filepath.Join(dir, "v1")
	v2 := filepath.Join(dir, "v2")
	if err := os.WriteFile(v1, []byte("NABD_PROVIDER=groq\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(v2, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvVar, v1)
	t.Setenv(V2EnvVar, v2)
	if _, _, err := SelectedPath(); err == nil {
		t.Fatal("v1 and v2 accepted together")
	}
}

func TestV2DisablesImplicitEnvironmentFallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GROQ_API_KEY", "secret")
	p := filepath.Join(t.TempDir(), "config.v2.json")
	body := `{"version":2,"provider":"anthropic","credentials":{"anthropic":{"source":"env"}}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(V2EnvVar, p)
	ResetForTest()
	t.Cleanup(ResetForTest)
	if err := Load(); err != nil {
		t.Fatal(err)
	}
	if got := Get("GROQ_API_KEY"); got != "" {
		t.Fatal("undeclared env credential leaked into v2")
	}
}

func TestV2CredentialFileAndClosedEndpointPolicy(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "config.v2.json")
	body := `{"version":2,"provider":"openrouter","credentials":{"openrouter":{"source":"file","path":"` + secret + `"}}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ParseV2File(p)
	if err != nil {
		t.Fatal(err)
	}
	if got["OPENROUTER_API_KEY"] != "file-secret" {
		t.Fatal("credential file not loaded")
	}
	unsafe := `{"version":2,"provider":"openrouter","base_url":"https://example.com/v1","credentials":{"openrouter":{"source":"file","path":"` + secret + `"}}}`
	if err := os.WriteFile(p, []byte(unsafe), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseV2File(p); err == nil {
		t.Fatal("minimal strict v2 accepted a custom endpoint")
	}
}

func TestV2ErrorsNeverContainCredentialValue(t *testing.T) {
	const sentinel = "never-print-v2-secret"
	t.Setenv("GROQ_API_KEY", sentinel)
	p := filepath.Join(t.TempDir(), "config.v2.json")
	if err := os.WriteFile(p, []byte(`{"version":2,"provider":"router","routes":[],"credentials":{"groq":{"source":"env"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ParseV2File(p)
	if err == nil || strings.Contains(err.Error(), sentinel) {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestV2CoexistenceOnDiskIsFatal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvVar, "")
	t.Setenv(V2EnvVar, "")
	ResetForTest()
	t.Cleanup(ResetForTest)

	agDir := filepath.Join(home, ".ag")
	if err := os.MkdirAll(agDir, 0o700); err != nil {
		t.Fatal(err)
	}
	v1Path := filepath.Join(agDir, "config")
	v2Path := filepath.Join(agDir, "config.v2.json")
	if err := os.WriteFile(v1Path, []byte("NABD_PROVIDER=groq\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(v2Path, []byte(`{"version":2,"provider":"groq","credentials":{"groq":{"source":"env"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Coexistence of both default files on disk MUST be fatal at startup.
	if _, _, err := SelectedPath(); err == nil || !strings.Contains(err.Error(), "cannot coexist") {
		t.Fatalf("SelectedPath with coexisting disk files err = %v, want 'cannot coexist'", err)
	}
	if err := Load(); err == nil || !strings.Contains(err.Error(), "cannot coexist") {
		t.Fatalf("Load with coexisting disk files err = %v, want 'cannot coexist'", err)
	}
	if got := Get("NABD_PROVIDER"); got != "" {
		t.Fatalf("Get with coexisting disk files returned %q, want empty (fails closed)", got)
	}

	// Removing v1 leaves v2 active.
	if err := os.Remove(v1Path); err != nil {
		t.Fatal(err)
	}
	ResetForTest()
	gotPath, ver, err := SelectedPath()
	if err != nil || ver != 2 || gotPath != v2Path {
		t.Fatalf("after removing v1: path=%s ver=%d err=%v", gotPath, ver, err)
	}

	// Restoring v1 and removing v2 leaves v1 active.
	if err := os.WriteFile(v1Path, []byte("NABD_PROVIDER=groq\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(v2Path); err != nil {
		t.Fatal(err)
	}
	ResetForTest()
	gotPath, ver, err = SelectedPath()
	if err != nil || ver != 1 || gotPath != v1Path {
		t.Fatalf("after removing v2: path=%s ver=%d err=%v", gotPath, ver, err)
	}
}

func TestV2RejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	realFile := filepath.Join(dir, "real.json")
	body := `{"version":2,"provider":"groq","credentials":{"groq":{"source":"env"}}}`
	if err := os.WriteFile(realFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkFile := filepath.Join(dir, "symlink.json")
	if err := os.Symlink(realFile, symlinkFile); err != nil {
		t.Skip("symlinks not supported in this environment")
	}
	if _, err := ParseV2File(symlinkFile); err == nil {
		t.Fatal("symlink to valid v2 config was accepted; want refusal")
	}
	// Direct regular file must be accepted.
	if _, err := ParseV2File(realFile); err != nil {
		t.Fatalf("direct file refused: %v", err)
	}
}

func TestV2CredentialFileRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	realSecret := filepath.Join(dir, "real-secret")
	if err := os.WriteFile(realSecret, []byte("my-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkSecret := filepath.Join(dir, "symlink-secret")
	if err := os.Symlink(realSecret, symlinkSecret); err != nil {
		t.Skip("symlinks not supported in this environment")
	}
	cfgPath := filepath.Join(dir, "config.v2.json")
	body := `{"version":2,"provider":"openrouter","credentials":{"openrouter":{"source":"file","path":"` + symlinkSecret + `"}}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseV2File(cfgPath); err == nil {
		t.Fatal("credential file that is a symlink was accepted; want refusal")
	}
}

func TestV2RejectsNonRegularFile(t *testing.T) {
	dir := t.TempDir()
	// Directory as config
	subDir := filepath.Join(dir, "dir_config")
	if err := os.MkdirAll(subDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseV2File(subDir); err == nil || !strings.Contains(err.Error(), "regular file required") {
		t.Fatalf("directory as config err = %v; want 'regular file required'", err)
	}

	// FIFO as config
	fifo := filepath.Join(dir, "fifo_config")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo not supported: %v", err)
	}
	if _, err := ParseV2File(fifo); err == nil || !strings.Contains(err.Error(), "regular file required") {
		t.Fatalf("FIFO as config err = %v; want 'regular file required'", err)
	}
}

func TestV2CredentialFileRejectsNonRegular(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo_cred")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo not supported: %v", err)
	}
	cfgPath := filepath.Join(dir, "config.v2.json")
	body := `{"version":2,"provider":"openrouter","credentials":{"openrouter":{"source":"file","path":"` + fifo + `"}}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseV2File(cfgPath); err == nil || !strings.Contains(err.Error(), "regular file required") {
		t.Fatalf("credential FIFO err = %v; want 'regular file required'", err)
	}
}

func TestV2RejectsInsecurePermissions(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.v2.json")
	body := `{"version":2,"provider":"groq","credentials":{"groq":{"source":"env"}}}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(p); err == nil && fi.Mode().Perm()&0o077 == 0 {
		t.Skip("filesystem or umask does not support loose permission bits")
	}
	if _, err := ParseV2File(p); err == nil {
		t.Fatal("config with 0644 permissions accepted; want refusal")
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseV2File(p); err != nil {
		t.Fatalf("config with 0600 permissions refused: %v", err)
	}
}

func TestV2CredentialFileRejectsInsecurePermissions(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("key-val\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(secret, 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(secret); err == nil && fi.Mode().Perm()&0o077 == 0 {
		t.Skip("filesystem or umask does not support loose permission bits")
	}
	cfgPath := filepath.Join(dir, "config.v2.json")
	body := `{"version":2,"provider":"openrouter","credentials":{"openrouter":{"source":"file","path":"` + secret + `"}}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseV2File(cfgPath); err == nil {
		t.Fatal("credential file with 0644 permissions accepted; want refusal")
	}
}

func TestV2RejectsWrongOwnership(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ownership check is a documented no-op on Windows")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "config.v2.json")
	body := `{"version":2,"provider":"groq","credentials":{"groq":{"source":"env"}}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// Happy path: owned by current user -> accepted
	if _, err := ParseV2File(p); err != nil {
		t.Fatalf("config owned by current user refused: %v", err)
	}
	if os.Getuid() != 0 {
		t.Skip("cannot chown to another uid without privilege; happy path verified")
	}
	if err := os.Chown(p, 1, 1); err != nil {
		t.Skipf("chown not permitted: %v", err)
	}
	if _, err := ParseV2File(p); err == nil {
		t.Fatal("config owned by another uid accepted; want refusal")
	}
}

func TestV2RejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "unknown_field",
			body:    `{"version":2,"provider":"groq","extra_field":"bad","credentials":{"groq":{"source":"env"}}}`,
			wantErr: "unknown field",
		},
		{
			name:    "trailing_json_object",
			body:    `{"version":2,"provider":"groq","credentials":{"groq":{"source":"env"}}} {"second":1}`,
			wantErr: "trailing JSON content",
		},
		{
			name:    "trailing_json_primitive",
			body:    `{"version":2,"provider":"groq","credentials":{"groq":{"source":"env"}}} 42`,
			wantErr: "trailing JSON content",
		},
		{
			name:    "trailing_garbage",
			body:    `{"version":2,"provider":"groq","credentials":{"groq":{"source":"env"}}} trailing_junk`,
			wantErr: "trailing JSON content",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, tc.name+".json")
			if err := os.WriteFile(p, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := ParseV2File(p)
			if err == nil {
				t.Fatalf("%s: accepted invalid v2; want error containing %q", tc.name, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("%s: err=%v, want error containing %q", tc.name, err, tc.wantErr)
			}
		})
	}
}

func TestV2CredentialHomeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	credDir := filepath.Join(home, ".ag", "credentials")
	if err := os.MkdirAll(credDir, 0o700); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(credDir, "my-key")
	if err := os.WriteFile(secretPath, []byte("expanded-secret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(home, "config.v2.json")
	body := `{"version":2,"provider":"openrouter","credentials":{"openrouter":{"source":"file","path":"~/.ag/credentials/my-key"}}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ParseV2File(cfgPath)
	if err != nil {
		t.Fatalf("ParseV2File with ~/ credential path failed: %v", err)
	}
	if got["OPENROUTER_API_KEY"] != "expanded-secret-value" {
		t.Fatalf("OPENROUTER_API_KEY=%q, want %q", got["OPENROUTER_API_KEY"], "expanded-secret-value")
	}

	// Relative path without ~/ must be rejected
	relBody := `{"version":2,"provider":"openrouter","credentials":{"openrouter":{"source":"file","path":"relative/my-key"}}}`
	if err := os.WriteFile(cfgPath, []byte(relBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseV2File(cfgPath); err == nil {
		t.Fatal("relative credential path accepted; want refusal")
	}
}

// writeBothDefaults creates a home containing the fatal coexistence state:
// both ~/.ag/config (v1) and ~/.ag/config.v2.json (v2) on disk.
func writeBothDefaults(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	agDir := filepath.Join(home, ".ag")
	if err := os.MkdirAll(agDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agDir, "config"), []byte("NABD_PROVIDER=groq\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agDir, "config.v2.json"),
		[]byte(`{"version":2,"provider":"groq","credentials":{"groq":{"source":"env"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func writeExplicit(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// P1: default v1/v2 coexistence is fatal only when neither version is explicit.
func TestV2ExplicitSelectionBypassesDefaultCoexistence(t *testing.T) {
	t.Run("both defaults, no explicit selection is fatal", func(t *testing.T) {
		home := writeBothDefaults(t)
		t.Setenv("HOME", home)
		t.Setenv(EnvVar, "")
		t.Setenv(V2EnvVar, "")
		ResetForTest()
		t.Cleanup(ResetForTest)
		if _, _, err := SelectedPath(); err == nil || !strings.Contains(err.Error(), "cannot coexist") {
			t.Fatalf("err=%v, want 'cannot coexist'", err)
		}
	})

	t.Run("explicit v1 wins over coexisting defaults", func(t *testing.T) {
		home := writeBothDefaults(t)
		t.Setenv("HOME", home)
		third := writeExplicit(t, "explicit-v1", "NABD_PROVIDER=groq\n")
		t.Setenv(EnvVar, third)
		t.Setenv(V2EnvVar, "")
		got, ver, err := SelectedPath()
		if err != nil || ver != 1 || got != third {
			t.Fatalf("path=%s ver=%d err=%v, want explicit v1 %s", got, ver, err, third)
		}
	})

	t.Run("explicit v2 wins over coexisting defaults", func(t *testing.T) {
		home := writeBothDefaults(t)
		t.Setenv("HOME", home)
		third := writeExplicit(t, "explicit-v2.json",
			`{"version":2,"provider":"groq","credentials":{"groq":{"source":"env"}}}`)
		t.Setenv(EnvVar, "")
		t.Setenv(V2EnvVar, third)
		got, ver, err := SelectedPath()
		if err != nil || ver != 2 || got != third {
			t.Fatalf("path=%s ver=%d err=%v, want explicit v2 %s", got, ver, err, third)
		}
	})

	t.Run("migration window: explicit override beats both defaults", func(t *testing.T) {
		home := writeBothDefaults(t)
		t.Setenv("HOME", home)
		third := writeExplicit(t, "migration", "NABD_PROVIDER=groq\n")
		t.Setenv(EnvVar, third)
		t.Setenv(V2EnvVar, "")
		got, ver, err := SelectedPath()
		if err != nil || ver != 1 || got != third {
			t.Fatalf("path=%s ver=%d err=%v, want %s", got, ver, err, third)
		}
	})

	t.Run("explicit conflict contract unchanged", func(t *testing.T) {
		home := writeBothDefaults(t)
		t.Setenv("HOME", home)
		v1 := writeExplicit(t, "v1", "NABD_PROVIDER=groq\n")
		v2 := writeExplicit(t, "v2", `{"version":2}`)
		t.Setenv(EnvVar, v1)
		t.Setenv(V2EnvVar, v2)
		if _, _, err := SelectedPath(); err == nil || !strings.Contains(err.Error(), "cannot be selected together") {
			t.Fatalf("err=%v, want 'cannot be selected together'", err)
		}
	})
}

// P2: when default discovery is required, an unresolvable home must fail
// closed instead of silently skipping the coexistence guard. android's
// os.UserHomeDir falls back to /sdcard instead of failing, so the
// home-resolution failure is driven through the injected resolver seam.
func TestV2HomeResolutionFailureFailsClosed(t *testing.T) {
	failingHome := func() (string, error) { return "", errors.New("no home available") }

	t.Run("no explicit selection fails closed at the discovery gate", func(t *testing.T) {
		t.Setenv(EnvVar, "")
		t.Setenv(V2EnvVar, "")
		_, _, err := selectedPath(failingHome)
		if err == nil {
			t.Fatal("home resolution failure was silently skipped; want error")
		}
		if !strings.Contains(err.Error(), "cannot resolve home directory") {
			t.Fatalf("err=%v, want discovery-gate failure context", err)
		}
	})

	t.Run("explicit absolute v1 does not need home", func(t *testing.T) {
		p := writeExplicit(t, "v1", "NABD_PROVIDER=groq\n")
		t.Setenv(EnvVar, p)
		t.Setenv(V2EnvVar, "")
		got, ver, err := selectedPath(failingHome)
		if err != nil || ver != 1 || got != p {
			t.Fatalf("path=%s ver=%d err=%v, want %s", got, ver, err, p)
		}
	})

	t.Run("explicit absolute v2 does not need home", func(t *testing.T) {
		p := writeExplicit(t, "v2.json", `{"version":2,"provider":"groq","credentials":{"groq":{"source":"env"}}}`)
		t.Setenv(EnvVar, "")
		t.Setenv(V2EnvVar, p)
		got, ver, err := selectedPath(failingHome)
		if err != nil || ver != 2 || got != p {
			t.Fatalf("path=%s ver=%d err=%v, want %s", got, ver, err, p)
		}
	})

	t.Run("explicit ~ credential expansion fails closed without home", func(t *testing.T) {
		_, err := resolveCredentialWithHome("openrouter", "OPENROUTER_API_KEY",
			V2Credential{Source: "file", Path: "~/credentials.json"}, failingHome)
		if err == nil || !strings.Contains(err.Error(), "home expansion failed") {
			t.Fatalf("err=%v, want 'home expansion failed'", err)
		}
	})
}

// P3: only the exact "~/" prefix expands; bare "~" and "~user/..." are rejected
// with a message that states the accepted forms.
func TestV2CredentialHomeExpansionStrict(t *testing.T) {
	writeSecret := func(t *testing.T, home, rel, value string) {
		t.Helper()
		full := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeCfg := func(t *testing.T, home, credPath string) string {
		t.Helper()
		p := filepath.Join(home, "config.v2.json")
		body := `{"version":2,"provider":"openrouter","credentials":{"openrouter":{"source":"file","path":"` + credPath + `"}}}`
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("~/ .ag path expands", func(t *testing.T) {
		home := t.TempDir()
		writeSecret(t, home, filepath.Join(".ag", "credentials", "my-key"), "expanded-secret-value")
		t.Setenv("HOME", home)
		got, err := ParseV2File(writeCfg(t, home, "~/.ag/credentials/my-key"))
		if err != nil || got["OPENROUTER_API_KEY"] != "expanded-secret-value" {
			t.Fatalf("got=%q err=%v", got["OPENROUTER_API_KEY"], err)
		}
	})

	t.Run("~/filename expands", func(t *testing.T) {
		home := t.TempDir()
		writeSecret(t, home, "credentials.json", "root-secret")
		t.Setenv("HOME", home)
		got, err := ParseV2File(writeCfg(t, home, "~/credentials.json"))
		if err != nil || got["OPENROUTER_API_KEY"] != "root-secret" {
			t.Fatalf("got=%q err=%v", got["OPENROUTER_API_KEY"], err)
		}
	})

	t.Run("bare ~ rejected", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		_, err := ParseV2File(writeCfg(t, home, "~"))
		if err == nil || !strings.Contains(err.Error(), "or start with ~/") {
			t.Fatalf("err=%v, want rejection naming the ~/ form", err)
		}
	})

	t.Run("~user path rejected", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		_, err := ParseV2File(writeCfg(t, home, "~user/credentials.json"))
		if err == nil || !strings.Contains(err.Error(), "or start with ~/") {
			t.Fatalf("err=%v, want rejection naming the ~/ form", err)
		}
	})

	t.Run("ordinary relative path rejected", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		_, err := ParseV2File(writeCfg(t, home, "relative/my-key"))
		if err == nil || !strings.Contains(err.Error(), "or start with ~/") {
			t.Fatalf("err=%v, want rejection naming the ~/ form", err)
		}
	})

	t.Run("absolute path accepted unchanged", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		abs := filepath.Join(t.TempDir(), "abs-secret")
		if err := os.WriteFile(abs, []byte("abs-value\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := ParseV2File(writeCfg(t, home, abs))
		if err != nil || got["OPENROUTER_API_KEY"] != "abs-value" {
			t.Fatalf("got=%q err=%v", got["OPENROUTER_API_KEY"], err)
		}
	})

	t.Run("~/ with unavailable home fails closed", func(t *testing.T) {
		_, err := resolveCredentialWithHome("openrouter", "OPENROUTER_API_KEY",
			V2Credential{Source: "file", Path: "~/credentials.json"},
			func() (string, error) { return "", errors.New("no home available") })
		if err == nil || !strings.Contains(err.Error(), "home expansion failed") {
			t.Fatalf("err=%v, want 'home expansion failed'", err)
		}
	})

	t.Run("invalid input error names the ~/ form", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		_, err := ParseV2File(writeCfg(t, home, "relative/my-key"))
		if err == nil || !strings.Contains(err.Error(), "~/") {
			t.Fatalf("err=%v, want message mentioning ~/", err)
		}
	})
}
