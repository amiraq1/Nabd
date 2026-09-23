package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemovedBashKeysRequestingBoundaryFailClosed asserts that setting any of
// the removed Landlock sandbox keys to a boundary-enforcing value (on, deny,
// limit) fails closed immediately at configuration load time with an explicit
// error instructing the user to remove the setting.
func TestRemovedBashKeysRequestingBoundaryFailClosed(t *testing.T) {
	cases := []struct {
		key string
		val string
	}{
		{"NABD_BASH_SANDBOX", "on"},
		{"NABD_BASH_SANDBOX", "ON"},
		{"NABD_BASH_SANDBOX", " on "},
		{"NABD_BASH_NETWORK", "deny"},
		{"NABD_BASH_NETWORK", "Deny"},
		{"NABD_BASH_NETWORK", " DENY "},
		{"NABD_BASH_RESOURCES", "limit"},
		{"NABD_BASH_RESOURCES", "Limit"},
		{"NABD_BASH_RESOURCES", " LIMIT "},
	}

	for _, tc := range cases {
		// 1. Environment variable source
		t.Run("env_"+tc.key+"_"+strings.TrimSpace(tc.val), func(t *testing.T) {
			ResetForTest()
			t.Cleanup(ResetForTest)
			t.Setenv(EnvVar, filepath.Join(t.TempDir(), "missing-config"))
			t.Setenv(V2EnvVar, "")
			t.Setenv(tc.key, tc.val)

			err := Load()
			if err == nil {
				t.Fatalf("Load() succeeded; expected fail-closed error for %s=%q", tc.key, tc.val)
			}
			wantSub := fmt.Sprintf("%s=%s is no longer supported: this Termux build has no bash sandbox. Remove the setting to continue", tc.key, strings.TrimSpace(tc.val))
			if !strings.Contains(err.Error(), wantSub) {
				t.Fatalf("Load() error = %q, want substring %q", err.Error(), wantSub)
			}
		})

		// 2. V1 file source
		t.Run("v1_"+tc.key+"_"+strings.TrimSpace(tc.val), func(t *testing.T) {
			ResetForTest()
			t.Cleanup(ResetForTest)
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config")
			content := fmt.Sprintf("NABD_PROVIDER=groq\n%s=%s\n", tc.key, tc.val)
			if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(EnvVar, cfgPath)
			t.Setenv(V2EnvVar, "")
			t.Setenv(tc.key, "")

			err := Load()
			if err == nil {
				t.Fatalf("Load() succeeded; expected fail-closed error for %s=%q in v1 file", tc.key, tc.val)
			}
			wantSub := fmt.Sprintf("%s=%s is no longer supported: this Termux build has no bash sandbox. Remove the setting to continue", tc.key, strings.TrimSpace(tc.val))
			if !strings.Contains(err.Error(), wantSub) {
				t.Fatalf("Load() error = %q, want substring %q", err.Error(), wantSub)
			}
		})

		// 3. V2 file source
		t.Run("v2_"+tc.key+"_"+strings.TrimSpace(tc.val), func(t *testing.T) {
			ResetForTest()
			t.Cleanup(ResetForTest)
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.v2.json")
			content := fmt.Sprintf(`{"version":2,"provider":"groq","credentials":{"groq":{"source":"env"}},%q:%q}`, tc.key, strings.TrimSpace(tc.val))
			if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(EnvVar, "")
			t.Setenv(V2EnvVar, cfgPath)
			t.Setenv(tc.key, "")

			err := Load()
			if err == nil {
				t.Fatalf("Load() succeeded; expected fail-closed error for %s=%q in v2 file", tc.key, tc.val)
			}
			wantSub := fmt.Sprintf("%s=%s is no longer supported: this Termux build has no bash sandbox. Remove the setting to continue", tc.key, strings.TrimSpace(tc.val))
			if !strings.Contains(err.Error(), wantSub) {
				t.Fatalf("Load() error = %q, want substring %q", err.Error(), wantSub)
			}
		})
	}
}

// TestRemovedBashKeysNeutralValuesWarnOnly asserts that neutral values (auto,
// off, allow, empty) for the removed keys do not abort configuration loading,
// but emit a one-line warning on stderr explaining that the key is ignored.
func TestRemovedBashKeysNeutralValuesWarnOnly(t *testing.T) {
	cases := []struct {
		key string
		val string
	}{
		{"NABD_BASH_SANDBOX", "auto"},
		{"NABD_BASH_SANDBOX", "off"},
		{"NABD_BASH_SANDBOX", ""},
		{"NABD_BASH_NETWORK", "allow"},
		{"NABD_BASH_NETWORK", ""},
		{"NABD_BASH_RESOURCES", "allow"},
		{"NABD_BASH_RESOURCES", ""},
	}

	for _, tc := range cases {
		// Environment source
		t.Run("env_"+tc.key+"_"+tc.val, func(t *testing.T) {
			ResetForTest()
			t.Cleanup(ResetForTest)
			var buf bytes.Buffer
			Stderr = &buf
			t.Cleanup(func() { Stderr = os.Stderr })
			t.Setenv(EnvVar, filepath.Join(t.TempDir(), "missing-config"))
			t.Setenv(V2EnvVar, "")
			t.Setenv(tc.key, tc.val)

			if err := Load(); err != nil {
				t.Fatalf("Load() failed unexpectedly for neutral value %s=%q: %v", tc.key, tc.val, err)
			}
			wantWarn := fmt.Sprintf("warning: %s is ignored: this Termux build has no bash sandbox\n", tc.key)
			if !strings.Contains(buf.String(), wantWarn) {
				t.Fatalf("expected warning %q, got %q", wantWarn, buf.String())
			}
		})

		// V1 file source
		t.Run("v1_"+tc.key+"_"+tc.val, func(t *testing.T) {
			ResetForTest()
			t.Cleanup(ResetForTest)
			var buf bytes.Buffer
			Stderr = &buf
			t.Cleanup(func() { Stderr = os.Stderr })
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config")
			content := fmt.Sprintf("NABD_PROVIDER=groq\n%s=%s\n", tc.key, tc.val)
			if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(EnvVar, cfgPath)
			t.Setenv(V2EnvVar, "")
			t.Setenv(tc.key, "")

			if err := Load(); err != nil {
				t.Fatalf("Load() failed unexpectedly for neutral value %s=%q: %v", tc.key, tc.val, err)
			}
			wantWarn := fmt.Sprintf("warning: %s is ignored: this Termux build has no bash sandbox\n", tc.key)
			if !strings.Contains(buf.String(), wantWarn) {
				t.Fatalf("expected warning %q, got %q", wantWarn, buf.String())
			}
		})

		// V2 file source
		t.Run("v2_"+tc.key+"_"+tc.val, func(t *testing.T) {
			ResetForTest()
			t.Cleanup(ResetForTest)
			var buf bytes.Buffer
			Stderr = &buf
			t.Cleanup(func() { Stderr = os.Stderr })
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.v2.json")
			content := fmt.Sprintf(`{"version":2,"provider":"groq","credentials":{"groq":{"source":"env"}},%q:%q}`, tc.key, tc.val)
			if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(EnvVar, "")
			t.Setenv(V2EnvVar, cfgPath)
			t.Setenv(tc.key, "")

			if err := Load(); err != nil {
				t.Fatalf("Load() failed unexpectedly for neutral value %s=%q: %v", tc.key, tc.val, err)
			}
			wantWarn := fmt.Sprintf("warning: %s is ignored: this Termux build has no bash sandbox\n", tc.key)
			if !strings.Contains(buf.String(), wantWarn) {
				t.Fatalf("expected warning %q, got %q", wantWarn, buf.String())
			}
		})
	}
}
