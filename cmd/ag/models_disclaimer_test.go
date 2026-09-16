package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/config"
)

// TestModelsSuccessDoesNotImplyValidKey pins the limit found in the field: a
// catalog endpoint may answer GET /models with 200 for any credential, so a
// successful listing is not evidence that the key works. The command must still
// send the key and must say what the listing does and does not prove.
func TestModelsSuccessDoesNotImplyValidKey(t *testing.T) {
	var sawAuthorization string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuthorization = r.Header.Get("authorization")
		if r.URL.Path != "/models" {
			t.Errorf("path = %q, want /models", r.URL.Path)
		}
		// Deliberately unconditional: this endpoint does not authenticate.
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"acme-1"}]}`)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	provPath := filepath.Join(dir, "providers.json")
	authPath := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(provPath, []byte(`{
  "provider": {
    "acme": {
      "api": "openai",
      "options": { "baseURL": "`+srv.URL+`" },
      "defaultModel": "acme-1",
      "models": { "acme-1": { "name": "Acme One" } }
    }
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authPath, []byte(`{"acme":{"type":"api","key":"definitely-not-a-valid-key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("NABD_CONFIG", filepath.Join(dir, "config-absent"))
	t.Setenv("NABD_PROVIDERS_FILE", provPath)
	t.Setenv("NABD_AUTH_FILE", authPath)
	config.ResetForTest()
	t.Cleanup(config.ResetForTest)

	var out, errOut bytes.Buffer
	if code := runModelsCommand([]string{"acme"}, &out, &errOut, srv.Client()); code != 0 {
		t.Fatalf("runModelsCommand exit = %d, stderr: %s", code, errOut.String())
	}

	if !strings.Contains(out.String(), "acme-1") {
		t.Errorf("stdout = %q, want the listed model id", out.String())
	}
	if sawAuthorization == "" {
		t.Error("the key was not sent; the test would not prove that a 200 says nothing about it")
	}
	if !strings.Contains(errOut.String(), "does not prove") {
		t.Errorf("stderr must state that a catalog 200 is not a credential check, got %q", errOut.String())
	}
}
