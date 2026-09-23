package providercmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"nabd/internal/endpoint"
	"nabd/internal/provider"
	"nabd/internal/redact"
)

// maxCatalogBytes bounds a /models response so a hostile or broken endpoint
// cannot make the command read without limit.
const maxCatalogBytes = 1 << 20

// CatalogIsNotACredentialCheck is printed after a successful listing. A 200
// from /models proves the catalog is readable, not that the key is valid: some
// endpoints answer it without authenticating at all, so a bogus key still gets
// 200. The first inference request is the real check.
const CatalogIsNotACredentialCheck = "note: a successful /models listing does not prove the API key is valid — " +
	"some endpoints answer it without authenticating. The first inference request is the real check."

// ModelCatalog is the shared shape of an OpenAI- or Anthropic-style /models
// response: a data array of objects carrying an id.
type ModelCatalog struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// ParseModelsResponse extracts the model identifiers from a /models body,
// dropping blanks and sorting so the output is stable across providers.
func ParseModelsResponse(body []byte) ([]string, error) {
	var c ModelCatalog
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, fmt.Errorf("models: %w", err)
	}
	ids := make([]string, 0, len(c.Data))
	for _, m := range c.Data {
		if id := strings.TrimSpace(m.ID); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, errors.New("models: response carried no model ids")
	}
	sort.Strings(ids)
	return ids, nil
}

// FetchModels lists the models an endpoint advertises. The dialect selects the
// auth header: anthropic uses x-api-key, everything else Bearer. The returned
// error is classified like the runtime provider errors; see KindOf.
func FetchModels(ctx context.Context, dialect, baseURL, key string, client *http.Client) ([]string, error) {
	if client == nil {
		client = endpoint.Client(0)
	}
	url := strings.TrimRight(baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if dialect == "anthropic" {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("authorization", "Bearer "+key)
	}
	req.Header.Set("accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, classified{kind: provider.ErrorKindTemporary, err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, classified{
			kind: provider.ClassifyHTTPStatus(resp.StatusCode),
			err: fmt.Errorf("http %d: %s", resp.StatusCode,
				redact.SanitizeBody(string(body), nil)),
		}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes))
	if err != nil {
		return nil, classified{kind: provider.ErrorKindTemporary, err: err}
	}
	return ParseModelsResponse(body)
}

// classified carries a provider error kind for errors raised outside the
// request path, so the command can print the existing vocabulary without
// importing the agent package.
type classified struct {
	kind provider.ErrorKind
	err  error
}

func (c classified) Error() string { return c.err.Error() }
func (c classified) Unwrap() error { return c.err }

// ErrorKind exposes the out-of-band classification to provider.ErrorKindOf (and
// therefore agent.ErrorCodeOf), so a caller can classify a provider error with
// the one canonical classifier instead of a second, local switch.
func (c classified) ErrorKind() provider.ErrorKind { return c.kind }

// KindOf reports the provider error kind for errors from this package, then
// falls back to provider.ErrorKindOf for anything it did not classify.
func KindOf(err error) provider.ErrorKind {
	return provider.ErrorKindOf(err)
}
