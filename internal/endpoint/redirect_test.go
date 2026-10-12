package endpoint_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nabd/internal/endpoint"
)

// TestClientRefusesRedirectLeak is the P0-1 regression test: a provider client
// built by endpoint.Client must refuse HTTP redirects, and its credential
// (x-api-key) must never be forwarded to the redirect target.
//
// Status cases: 302 (Found) and 307 (Temporary Redirect, method-preserving).
// Both previously leaked x-api-key to the second host under Go's default
// redirect behaviour, because Go only strips the canonical Authorization
// header, not custom credential headers.
func TestClientRefusesRedirectLeak(t *testing.T) {
	t.Setenv("NABD_ENDPOINT_POLICY", "open")

	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect} {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			var (
				leakHit  bool
				leakKey  string
				firstHit bool
			)
			leakTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				leakHit = true
				leakKey = r.Header.Get("x-api-key")
				w.WriteHeader(200)
			}))
			defer leakTarget.Close()

			redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				firstHit = true
				w.Header().Set("Location", leakTarget.URL)
				w.WriteHeader(status)
			}))
			defer redirector.Close()

			c := endpoint.Client(0)
			req, err := http.NewRequest("POST", redirector.URL, strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("x-api-key", "SUPERSECRETKEY")
			req.Header.Set("content-type", "application/json")

			resp, err := c.Do(req)
			if err == nil {
				t.Fatal("expected the endpoint client to refuse the redirect; got nil error")
			}
			if !errors.Is(err, endpoint.ErrRedirectRefused) {
				t.Fatalf("expected ErrRedirectRefused, got %v", err)
			}
			if resp != nil {
				_ = resp.Body.Close()
				t.Error("expected nil response when a redirect is refused")
			}
			if !firstHit {
				t.Fatal("redirector was never contacted; test setup is wrong")
			}
			if leakHit {
				t.Fatal("provider credential (x-api-key) leaked to the redirect target")
			}
			if leakKey != "" {
				t.Fatalf("x-api-key reached the redirect target: %q", leakKey)
			}
		})
	}
}

// TestEndpointClientTransportBlocksRedirectBypass proves the defense-in-depth
// control: even a caller that reuses the endpoint client's Transport and sets
// Client.CheckRedirect to follow redirects cannot leak the credential, because
// the wrapped transport refuses any redirect-following request outright.
func TestEndpointClientTransportBlocksRedirectBypass(t *testing.T) {
	t.Setenv("NABD_ENDPOINT_POLICY", "open")

	var leakHit bool
	leakTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leakHit = true
		w.WriteHeader(200)
	}))
	defer leakTarget.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", leakTarget.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer redirector.Close()

	// Reuse the endpoint client's (already wrapped) transport, but build a fresh
	// *http.Client whose CheckRedirect follows redirects — i.e. attempt to bypass
	// the client-level CheckRedirect.
	base := endpoint.Client(0)
	bypass := &http.Client{Transport: base.Transport}
	req, _ := http.NewRequest("POST", redirector.URL, strings.NewReader("{}"))
	req.Header.Set("x-api-key", "SUPERSECRETKEY")

	resp, err := bypass.Do(req)
	if err == nil {
		t.Fatal("expected the wrapped transport to refuse the redirect despite client-level bypass")
	}
	if !errors.Is(err, endpoint.ErrRedirectRefused) {
		t.Fatalf("expected ErrRedirectRefused from wrapped transport, got %v", err)
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
	if leakHit {
		t.Fatal("provider credential leaked to the redirect target through the bypass attempt")
	}
}

// TestCustomClientWithoutEndpointPackageCanFollowRedirect documents the boundary
// of the control: a caller that constructs an *http.Client entirely outside the
// endpoint package (and points it at a redirect) is not constrained by our
// CheckRedirect. All production providers use endpoint.Client(0), so they are
// covered; this case only guards against future code that intentionally leaves
// the package. It must keep redirecting (i.e. not refuse) so the assertion
// documents the expected, deliberately-out-of-scope behaviour.
func TestCustomClientWithoutEndpointPackageCanFollowRedirect(t *testing.T) {
	t.Setenv("NABD_ENDPOINT_POLICY", "open")

	var leakHit bool
	leakTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leakHit = true
		w.WriteHeader(200)
	}))
	defer leakTarget.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", leakTarget.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer redirector.Close()

	// Plain client with Go's default (follow) redirect behaviour.
	c := &http.Client{}
	req, _ := http.NewRequest("POST", redirector.URL, strings.NewReader("{}"))
	req.Header.Set("x-api-key", "SUPERSECRETKEY")

	_, err := c.Do(req)
	if err != nil {
		t.Fatalf("a client built outside the endpoint package is not constrained; Do returned %v", err)
	}
	if !leakHit {
		t.Fatal("expected the out-of-package client to follow the redirect (documents the boundary)")
	}
}
