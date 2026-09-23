package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/config"
	"nabd/internal/perm"
)

// TestInteractiveStartupHelper is the subprocess entry point for verifying that
// the real main() aborts before starting any interactive UI when removed sandbox
// keys request a boundary.
func TestInteractiveStartupHelper(t *testing.T) {
	if os.Getenv("TEST_INTERACTIVE_HELPER") != "1" {
		return
	}
	if raw := os.Getenv("TEST_INTERACTIVE_ARGS"); raw != "" {
		os.Args = append([]string{"nabd"}, strings.Fields(raw)...)
	} else {
		os.Args = []string{"nabd"}
	}
	main()
}

// TestRemovedBashKeysFailClosedInteractiveStartup asserts that all interactive
// entry points (default feed, --continue, --feed=false legacy chat) call
// config.Load() and abort fail-closed before any provider or tool runs when
// removed bash sandbox keys request a boundary.
func TestRemovedBashKeysFailClosedInteractiveStartup(t *testing.T) {
	cases := []struct {
		key string
		val string
	}{
		{"NABD_BASH_SANDBOX", "on"},
		{"NABD_BASH_NETWORK", "deny"},
		{"NABD_BASH_RESOURCES", "limit"},
	}

	for _, tc := range cases {
		// 1. Direct interactive entry point assertions
		t.Run("direct_"+tc.key, func(t *testing.T) {
			config.ResetForTest()
			t.Cleanup(config.ResetForTest)

			t.Setenv(config.EnvVar, filepath.Join(t.TempDir(), "missing-config"))
			t.Setenv(config.V2EnvVar, "")
			t.Setenv(tc.key, tc.val)

			dir := t.TempDir()
			wantSub := fmt.Sprintf("%s=%s is no longer supported: this Termux build has no bash sandbox. Remove the setting to continue", tc.key, tc.val)

			// Feed default
			errFeed := doChatWithFeed(perm.ModeAsk, dir, false, false)
			if errFeed == nil || !strings.Contains(errFeed.Error(), wantSub) {
				t.Fatalf("doChatWithFeed() err = %v, want substring %q", errFeed, wantSub)
			}

			// Feed continue
			errFeedCont := doChatWithFeed(perm.ModeAsk, dir, true, false)
			if errFeedCont == nil || !strings.Contains(errFeedCont.Error(), wantSub) {
				t.Fatalf("doChatWithFeed(--continue) err = %v, want substring %q", errFeedCont, wantSub)
			}

			// Chat default
			errChat := doChat(perm.ModeAsk, dir, false)
			if errChat == nil || !strings.Contains(errChat.Error(), wantSub) {
				t.Fatalf("doChat() err = %v, want substring %q", errChat, wantSub)
			}

			// Chat continue
			errChatCont := doChat(perm.ModeAsk, dir, true)
			if errChatCont == nil || !strings.Contains(errChatCont.Error(), wantSub) {
				t.Fatalf("doChat(--continue) err = %v, want substring %q", errChatCont, wantSub)
			}
		})

		// 2. Subprocess main() execution across CLI flags
		flagModes := []struct {
			name string
			args []string
		}{
			{"feed_default", []string{}},
			{"continue", []string{"--continue"}},
			{"legacy_chat", []string{"--feed=false"}},
		}

		for _, fm := range flagModes {
			t.Run("subprocess_"+fm.name+"_"+tc.key, func(t *testing.T) {
				cmd := exec.Command(os.Args[0], "-test.run=^TestInteractiveStartupHelper$")
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				cmd.Env = append(os.Environ(),
					"TEST_INTERACTIVE_HELPER=1",
					"TEST_INTERACTIVE_ARGS="+strings.Join(fm.args, " "),
					tc.key+"="+tc.val,
					config.EnvVar+"="+filepath.Join(t.TempDir(), "missing-config"),
					config.V2EnvVar+"=",
				)

				err := cmd.Run()
				if err == nil {
					t.Fatalf("subprocess main(%v) succeeded; expected fail-closed abort for %s=%s", fm.args, tc.key, tc.val)
				}
				wantMsg := fmt.Sprintf("nabd: %s=%s is no longer supported: this Termux build has no bash sandbox. Remove the setting to continue", tc.key, tc.val)
				if !strings.Contains(stderr.String(), wantMsg) {
					t.Fatalf("subprocess stderr = %q, want substring %q", stderr.String(), wantMsg)
				}
			})
		}
	}
}
