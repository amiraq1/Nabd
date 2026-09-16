package providercmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/provider"
	"nabd/internal/registry"
)

func TestConnectWritesAuthFileWithTightPermissions(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")

	summary, err := Connect(authPath, "novita", func() (string, error) {
		return "sk-novita-secret", nil
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if strings.Contains(summary, "sk-novita-secret") {
		t.Fatalf("summary leaked the key: %q", summary)
	}

	fi, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("auth file not written: %v", err)
	}
	if mode := fi.Mode().Perm(); mode != 0o600 {
		t.Errorf("auth file mode = %04o, want 0600", mode)
	}

	af, err := registry.ParseAuthFile(authPath)
	if err != nil {
		t.Fatalf("auth file is not readable back: %v", err)
	}
	if got := af["novita"].Key; got != "sk-novita-secret" {
		t.Errorf("stored key = %q, want the key from the hidden reader", got)
	}
}

func TestConnectRefusesKeyAsArgument(t *testing.T) {
	refused := [][]string{
		{"novita", "sk-novita-secret"},
		{"sk-novita-secret"},
		{"gsk_legacykey"},
		{"novita", "--key", "sk-x"},
	}
	for _, args := range refused {
		if _, err := ParseConnectArgs(args); !errors.Is(err, ErrKeyAsArgument) {
			t.Errorf("ParseConnectArgs(%v) = %v, want ErrKeyAsArgument", args, err)
		}
	}

	id, err := ParseConnectArgs([]string{"Novita"})
	if err != nil {
		t.Fatalf("a single provider name must be accepted: %v", err)
	}
	if id != "novita" {
		t.Errorf("provider id = %q, want novita", id)
	}

	if _, err := ParseConnectArgs(nil); err == nil {
		t.Error("no provider must be a usage error")
	}
	if _, err := ParseConnectArgs([]string{"Bad Provider"}); err == nil {
		t.Error("an invalid provider id must be rejected")
	}
}

func TestModelsCommandParsesCatalogResponse(t *testing.T) {
	body := []byte(`{"object":"list","data":[{"id":"qwen/qwen3.6-27b"},{"id":"llama-3.3-70b"},{"id":"   "}]}`)
	ids, err := ParseModelsResponse(body)
	if err != nil {
		t.Fatalf("ParseModelsResponse: %v", err)
	}
	want := []string{"llama-3.3-70b", "qwen/qwen3.6-27b"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
	}

	if _, err := ParseModelsResponse([]byte(`{"data":[]}`)); err == nil {
		t.Error("an empty catalog must be an error, not an empty success")
	}

	// A live round trip: the endpoint is the source of truth, and the request
	// must carry the key as a header, never as a query parameter or path.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("authorization"); got != "Bearer k-test" {
			t.Errorf("authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"b"},{"id":"a"}]}`))
	}))
	defer srv.Close()

	got, err := FetchModels(context.Background(), "openai", srv.URL+"/v1", "k-test", srv.Client())
	if err != nil {
		t.Fatalf("FetchModels: %v", err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v, want [a b]", got)
	}

	// A transport failure is classified temporary, like the runtime errors.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	if _, err := FetchModels(context.Background(), "openai", deadURL, "k", http.DefaultClient); KindOf(err) != provider.ErrorKindTemporary {
		t.Errorf("KindOf(network failure) = %v, want temporary", KindOf(err))
	}
}

func TestProviderCommandReportsMissingKeys(t *testing.T) {
	dir := t.TempDir()
	provPath := filepath.Join(dir, "providers.json")
	authPath := filepath.Join(dir, "auth.json")

	if err := os.WriteFile(provPath, []byte(`{
  "provider": {
    "novita": {
      "api": "openai",
      "name": "Novita",
      "options": { "baseURL": "https://api.novita.ai/openai/v1" },
      "models": { "qwen/qwen3.6-27b": { "name": "Qwen 3.6 27B" } }
    }
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}

	reg, err := registry.LoadFromFiles(provPath, authPath, func(k string) string {
		if k == "GROQ_API_KEY" {
			return "gsk_present"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("LoadFromFiles: %v", err)
	}

	out := FormatProviders(DescribeProviders(reg), reg)

	if !strings.Contains(out, "novita") || !strings.Contains(out, "def:providers.json") {
		t.Errorf("a provider from providers.json must be listed with its source:\n%s", out)
	}
	if !strings.Contains(out, authPath) {
		t.Errorf("a missing key must name the auth file %q:\n%s", authPath, out)
	}
	groqLine := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "groq") {
			groqLine = line
		}
	}
	if !strings.Contains(groqLine, "key:env") {
		t.Errorf("groq's key must be reported as coming from the environment, got %q", groqLine)
	}
	if strings.Contains(out, "gsk_present") {
		t.Errorf("the listing must never print a key value:\n%s", out)
	}
}
