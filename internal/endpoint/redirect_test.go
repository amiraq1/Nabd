package endpoint_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"nabd/internal/endpoint"
)

// TestP01_CheckRedirectConfigured verifies that all endpoint client constructors
// wire CheckRedirect to a non-nil function that refuses redirects.
func TestP01_CheckRedirectConfigured(t *testing.T) {
	clients := []*http.Client{
		endpoint.Client(5 * time.Second),
		endpoint.ClientWithPolicy(endpoint.Policy(0), 5*time.Second),
		endpoint.ClientWithAllow(endpoint.Policy(0), endpoint.CurrentAllowList(), 5*time.Second),
	}

	for i, c := range clients {
		if c.CheckRedirect == nil {
			t.Fatalf("client[%d].CheckRedirect must not be nil", i)
		}
		// Must return http.ErrUseLastResponse
		err := c.CheckRedirect(nil, nil)
		if err != http.ErrUseLastResponse {
			t.Fatalf("client[%d].CheckRedirect must return http.ErrUseLastResponse, got %v", i, err)
		}
	}
}

// TestP01_RedirectDoesNotLeakCredentials tests that across all HTTP redirect status
// codes (301, 302, 303, 307, 308) for both same-origin and cross-origin targets,
// the client refuses to follow redirects, and Server B never receives sensitive headers.
func TestP01_RedirectDoesNotLeakCredentials(t *testing.T) {
	t.Setenv("NABD_ENDPOINT_POLICY", "open")

	redirectCodes := []int{
		http.StatusMovedPermanently,  // 301
		http.StatusFound,             // 302
		http.StatusSeeOther,          // 303
		http.StatusTemporaryRedirect, // 307
		http.StatusPermanentRedirect, // 308
	}

	for _, code := range redirectCodes {
		t.Run(fmt.Sprintf("status_%d", code), func(t *testing.T) {
			var targetHit atomic.Int32
			var leakedKey atomic.Value
			var leakedAuth atomic.Value

			// Target Server B: must NEVER be reached by the client
			serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				targetHit.Add(1)
				leakedKey.Store(r.Header.Get("x-api-key"))
				leakedAuth.Store(r.Header.Get("Authorization"))
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"target":"reached"}`))
			}))
			defer serverB.Close()

			// Origin Server A: returns redirect to Server B
			serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, serverB.URL+"/v1/steal", code)
			}))
			defer serverA.Close()

			client := endpoint.Client(5 * time.Second)

			req, err := http.NewRequest(http.MethodPost, serverA.URL+"/v1/chat", nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			const secretKey = "sk-ant-canary-api-key-never-leak"
			const secretAuth = "Bearer secret-auth-token-canary"
			req.Header.Set("x-api-key", secretKey)
			req.Header.Set("Authorization", secretAuth)

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("client.Do failed: %v", err)
			}
			defer resp.Body.Close()

			// The client must return the redirect response directly without following it
			if resp.StatusCode != code {
				t.Fatalf("expected response status code %d, got %d", code, resp.StatusCode)
			}

			// Target server B must NOT have been called
			if hits := targetHit.Load(); hits != 0 {
				t.Fatalf("CRITICAL SECURITY LEAK (P0-1): redirect target was hit %d times! LeakedKey=%v LeakedAuth=%v",
					hits, leakedKey.Load(), leakedAuth.Load())
			}
		})
	}
}
