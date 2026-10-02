package endpoint

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// resetPolicyWarned clears the invalid-policy warning memo. It exists only
// in test code; production never resets the memo.
func resetPolicyWarned() {
	policyWarnMu.Lock()
	defer policyWarnMu.Unlock()
	policyWarned = map[string]struct{}{}
}

// capturePolicyStderr redirects os.Stderr for the duration of fn and
// returns what was written. The restore is deferred so a panic or a
// t.Fatalf inside fn cannot leak the redirection.
func capturePolicyStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()
	fn()
	w.Close()
	var buf strings.Builder
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("reading captured stderr: %v", err)
	}
	r.Close()
	return buf.String()
}

func TestClientWarnsOncePerInvalidPolicyValue(t *testing.T) {
	// The memo is process-wide; reset on both ends so this test is
	// independent of the execution order of the external test package.
	resetPolicyWarned()
	defer resetPolicyWarned()

	// Two consecutive Client() calls with the same invalid value produce
	// exactly one warning line.
	t.Setenv("NABD_ENDPOINT_POLICY", "permissive")
	var first, second *http.Client
	out1 := capturePolicyStderr(t, func() { first = Client(0) })
	out2 := capturePolicyStderr(t, func() { second = Client(0) })
	if n := strings.Count(out1, "NABD_ENDPOINT_POLICY"); n != 1 {
		t.Errorf("first call: got %d warning lines, want 1 (%q)", n, out1)
	}
	if !strings.Contains(out1, "permissive") || !strings.Contains(out1, "strict") {
		t.Errorf("warning %q does not name the value and the strict fallback", out1)
	}
	if out2 != "" {
		t.Errorf("second call with the same value warned again: %q", out2)
	}

	// A different invalid value warns again.
	t.Setenv("NABD_ENDPOINT_POLICY", "relaxed")
	var third *http.Client
	out3 := capturePolicyStderr(t, func() { third = Client(0) })
	if !strings.Contains(out3, "relaxed") || !strings.Contains(out3, "strict") {
		t.Errorf("warning %q does not name the new value and the strict fallback", out3)
	}

	// Every client built from an invalid value behaves as PolicyStrict:
	// a plaintext loopback endpoint is refused.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	for name, c := range map[string]*http.Client{"first": first, "second": second, "third": third} {
		_, err := c.Get(srv.URL)
		if err == nil {
			t.Errorf("%s client built from an invalid policy reached a plaintext loopback endpoint; fallback is not PolicyStrict", name)
			continue
		}
		if !errors.Is(err, ErrEndpointRefused) {
			t.Errorf("%s client: expected ErrEndpointRefused, got %v", name, err)
		}
	}
}
