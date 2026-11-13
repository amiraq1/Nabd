package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidatePkgPattern(t *testing.T) {
	root, reg := newTestRegistry(t)
	if err := os.Mkdir(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "./...", false},
		{".", ".", false},
		{"./", ".", false},
		{"./...", "./...", false},
		{"./pkg", "./pkg", false},
		{"./pkg/...", "./pkg/...", false},
		{"std", "", true},
		{"github.com/a/b", "", true},
		{"../x", "", true},
		{"./../x", "", true},
		{"./..", "", true},
		{"-run", "", true},
		{"/abs", "", true},
	}
	for _, tc := range cases {
		got, err := reg.validatePkgPattern(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("validatePkgPattern(%q) = %q, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("validatePkgPattern(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("validatePkgPattern(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSummarizeGoTest(t *testing.T) {
	pass := summarizeGoTest("PASS", "ok  example.com/a  0.1s\nok  example.com/b  0.2s", nil)
	if !strings.HasPrefix(pass, "PASS") || !strings.Contains(pass, "ok  example.com/a") {
		t.Fatalf("pass summary = %q", pass)
	}

	fail := summarizeGoTest("FAIL",
		"--- FAIL: TestA (a_test.go:12)\n    a_test.go:13: boom\nFAIL\nexit status 1",
		errors.New("exit status 1"))
	for _, want := range []string{"FAIL", "--- FAIL: TestA", "a_test.go:13", "exit status 1"} {
		if !strings.Contains(fail, want) {
			t.Errorf("fail summary missing %q:\n%s", want, fail)
		}
	}

	build := summarizeGoTest("FAIL", "# example\n./x.go:3: undefined: y\nFAIL", errors.New("exit status 1"))
	if !strings.Contains(build, "./x.go:3") {
		t.Errorf("build summary missing file:line:\n%s", build)
	}

	timeout := summarizeGoTest("FAIL (timeout)", "", context.DeadlineExceeded)
	if !strings.Contains(timeout, "timeout") || !strings.Contains(timeout, "no output") {
		t.Errorf("timeout summary = %q", timeout)
	}

	long := summarizeGoTest("FAIL", strings.Repeat("x", 10000), nil)
	if len(long) > maxOutputBytes {
		t.Errorf("long summary = %d bytes", len(long))
	}
	if !strings.Contains(long, "...") {
		t.Errorf("long line was not clipped")
	}
}

func TestGoVerifyTimeout(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "go.mod"), "module audittimeout\n\ngo 1.22\n")
	writeTestFile(t, filepath.Join(root, "slow_test.go"), `package audittimeout

import (
	"testing"
	"time"
)

func TestSlow(t *testing.T) {
	time.Sleep(10 * time.Second)
}
`)

	start := time.Now()
	out, err := reg.runGoVerify(context.Background(), "./...", 1*time.Second)
	if err != nil {
		t.Fatalf("runGoVerify: %v", err)
	}
	if !strings.Contains(out, "timeout") {
		t.Fatalf("timeout summary = %q", out)
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("timeout took %v, process tree may still be running", elapsed)
	}
}

func TestGoVerifyTestFailureIsNotToolError(t *testing.T) {
	root, reg := newTestRegistry(t)
	writeTestFile(t, filepath.Join(root, "go.mod"), "module auditfail\n\ngo 1.22\n")
	writeTestFile(t, filepath.Join(root, "fail_test.go"), `package auditfail

import "testing"

func TestFail(t *testing.T) {
	t.Fatal("expected failure")
}
`)

	out := runToolApproved(t, reg, "go_verify", map[string]any{"pkg": "./..."})
	if strings.HasPrefix(out, "Error:") {
		t.Fatalf("test failure was reported as a tool error:\n%s", out)
	}
	if !strings.HasPrefix(out, "FAIL") || !strings.Contains(out, "--- FAIL: TestFail") {
		t.Fatalf("structured failure missing:\n%s", out)
	}
}
