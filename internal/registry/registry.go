// Package registry manages provider definitions, models, and authentication credentials.
//
// Registry Precedence:
//
// 1. Provider Definitions:
//   - ~/.ag/providers.json (highest precedence): Custom user-defined providers
//     or overrides for builtin providers.
//   - Builtin Catalog (fallback): Default definitions for canonical providers
//     (anthropic, groq, openrouter, nvidia).
//     A provider definition in providers.json completely overrides the builtin
//     definition for that provider ID.
//
// 2. Provider Credentials (API Keys):
//   - ~/.ag/auth.json (highest precedence): Secrets stored in the dedicated,
//     file-permission-checked 0600 credentials file.
//   - Environment variables and v1 config (fallback): Legacy credential variables
//     (ANTHROPIC_API_KEY, GROQ_API_KEY, OPENROUTER_API_KEY, NVIDIA_API_KEY).
//     A secret defined in auth.json overrides legacy environment variables.
package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"nabd/internal/config"
)

// PrecedenceDocumentation documents the authoritative loading precedence rules.
const PrecedenceDocumentation = `Registry Precedence:
1. Provider Definitions: providers.json > Builtin Catalog.
2. Provider Credentials: auth.json > Legacy Environment Variables / v1 Config.`

// ProvidersFile represents the schema of ~/.ag/providers.json.
type ProvidersFile struct {
	Provider map[string]ProviderConfig `json:"provider"`
}

// ProviderConfig represents configuration for one provider in providers.json.
type ProviderConfig struct {
	API     string                 `json:"api"`
	Name    string                 `json:"name,omitempty"`
	Options ProviderOptions        `json:"options,omitempty"`
	Models  map[string]ModelConfig `json:"models,omitempty"`
	ReadCap int                    `json:"readCap,omitempty"`
	// DefaultModel is the model used when nothing overrides it. It is what the
	// deleted per-provider constructors used to hard-code.
	DefaultModel string `json:"defaultModel,omitempty"`
}

// ProviderOptions carries options such as custom base URLs.
type ProviderOptions struct {
	BaseURL string `json:"baseURL,omitempty"`
}

// ModelConfig configures a model, with optional display name and wire ID alias.
type ModelConfig struct {
	Name string `json:"name,omitempty"`
	ID   string `json:"id,omitempty"`
}

// Provider represents a fully resolved provider in the registry.
type Provider struct {
	ID      string
	API     string // "openai" | "anthropic"
	Name    string
	BaseURL string
	Models  map[string]Model
	ReadCap int
	// DefaultModel is the model to use when the caller does not name one.
	DefaultModel string
	Key          string // resolved credential
	Source       string // "providers.json" | "builtin"
	KeySource    string // "auth.json" | "env" | "config" | "none"
}

// Model represents a resolved model configuration.
type Model struct {
	Key  string // map key under models
	Name string // human-friendly name
	ID   string // wire model ID (defaults to Key if empty)
}

// ResolveModelID returns the wire model ID to be sent to the provider.
// If an explicit alias ID was configured, it is returned; otherwise the model key is returned.
func (p Provider) ResolveModelID(modelKey string) string {
	if m, ok := p.Models[modelKey]; ok && m.ID != "" {
		return m.ID
	}
	return modelKey
}

// Registry holds the loaded providers and models.
type Registry struct {
	providers map[string]Provider
	// ProvidersPath and AuthPath are the resolved files the registry was loaded
	// from. They are reported by errors and by /provider so the user is told
	// where a missing provider or key belongs instead of having to guess.
	ProvidersPath string
	AuthPath      string
}

// DefaultPaths returns the standard user paths for providers.json and auth.json.
func DefaultPaths() (providersPath, authPath string, err error) {
	if p := strings.TrimSpace(os.Getenv("NABD_PROVIDERS_FILE")); p != "" {
		providersPath = filepath.Clean(p)
	}
	if a := strings.TrimSpace(os.Getenv("NABD_AUTH_FILE")); a != "" {
		authPath = filepath.Clean(a)
	}
	if providersPath != "" && authPath != "" {
		return providersPath, authPath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	if providersPath == "" {
		providersPath = filepath.Join(home, ".ag", "providers.json")
	}
	if authPath == "" {
		authPath = filepath.Join(home, ".ag", "auth.json")
	}
	return providersPath, authPath, nil
}

// Load loads the registry using default file paths and environment variables.
func Load() (*Registry, error) {
	provPath, authPath, err := DefaultPaths()
	if err != nil {
		return nil, err
	}
	return LoadFromFiles(provPath, authPath, func(k string) string {
		return config.Get(k)
	})
}

// LoadFromFiles loads and merges the builtin catalog, providers.json, and auth.json.
// Precedence:
// - Provider definitions: providers.json > Builtin Catalog
// - Credentials: auth.json > Legacy Environment Variables / v1 Config
func LoadFromFiles(providersPath, authPath string, envLookup func(string) string) (*Registry, error) {
	if envLookup == nil {
		envLookup = os.Getenv
	}

	// 1. Start with Builtin Catalog
	providers := make(map[string]Provider)
	for id, cfg := range BuiltinCatalog() {
		providers[id] = toProvider(id, cfg, "builtin")
	}

	// 2. Load providers.json (if present) - overrides Builtin
	userProviders, err := ParseProvidersFile(providersPath)
	if err != nil {
		return nil, err
	}
	for id, cfg := range userProviders {
		providers[id] = toProvider(id, cfg, "providers.json")
	}

	// 3. Load auth.json (if present)
	authFile, err := ParseAuthFile(authPath)
	if err != nil {
		return nil, err
	}

	// 4. Resolve credentials for all providers
	// Precedence: auth.json > envLookup (legacy env / v1 config)
	for id, prov := range providers {
		if authCfg, ok := authFile[id]; ok && authCfg.Key != "" {
			prov.Key = authCfg.Key
			prov.KeySource = "auth.json"
		} else {
			// Check legacy environment variable
			legacyVar := LegacyEnvKey(id)
			if legacyVar == "" {
				legacyVar = strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_API_KEY"
			}
			if val := strings.TrimSpace(envLookup(legacyVar)); val != "" {
				prov.Key = val
				prov.KeySource = "env"
			} else {
				prov.Key = ""
				prov.KeySource = "none"
			}
		}
		providers[id] = prov
	}

	return &Registry{providers: providers, ProvidersPath: providersPath, AuthPath: authPath}, nil
}

func toProvider(id string, cfg ProviderConfig, source string) Provider {
	readCap := cfg.ReadCap
	if readCap <= 0 {
		readCap = DefaultReadCapBytes
	}
	models := make(map[string]Model, len(cfg.Models))
	for mKey, mCfg := range cfg.Models {
		wireID := mCfg.ID
		if wireID == "" {
			wireID = mKey
		}
		models[mKey] = Model{
			Key:  mKey,
			Name: mCfg.Name,
			ID:   wireID,
		}
	}
	return Provider{
		ID:           id,
		API:          cfg.API,
		Name:         cfg.Name,
		BaseURL:      cfg.Options.BaseURL,
		Models:       models,
		ReadCap:      readCap,
		DefaultModel: cfg.DefaultModel,
		Source:       source,
	}
}

// Get returns the Provider with the given ID.
func (r *Registry) Get(id string) (Provider, bool) {
	if r == nil || r.providers == nil {
		return Provider{}, false
	}
	p, ok := r.providers[id]
	return p, ok
}

// ResolveModelID resolves a model alias or returns the model key for a provider.
func (r *Registry) ResolveModelID(providerID, modelKey string) (string, error) {
	p, ok := r.Get(providerID)
	if !ok {
		return "", fmt.Errorf("unknown provider %q", providerID)
	}
	return p.ResolveModelID(modelKey), nil
}

// Providers returns all resolved providers sorted by ID.
func (r *Registry) Providers() []Provider {
	if r == nil || r.providers == nil {
		return nil
	}
	res := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		res = append(res, p)
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].ID < res[j].ID
	})
	return res
}

// ProviderIDs returns sorted provider IDs.
func (r *Registry) ProviderIDs() []string {
	if r == nil || r.providers == nil {
		return nil
	}
	ids := make([]string, 0, len(r.providers))
	for id := range r.providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ParseProvidersFile securely opens, validates permissions (0600), and parses providers.json.
// Returns an empty map if the file does not exist.
func ParseProvidersFile(path string) (map[string]ProviderConfig, error) {
	data, err := readSecureFile(path, MaxFileBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseProvidersData(data)
}

// ParseProvidersData parses providers JSON data strictly.
func ParseProvidersData(data []byte) (map[string]ProviderConfig, error) {
	// Rule 5: Reject any literal key or credential in providers.json.
	if err := checkNoLiteralKeys(data); err != nil {
		return nil, err
	}

	var pf ProvidersFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pf); err != nil {
		return nil, fmt.Errorf("providers.json: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("providers.json: trailing JSON content")
	}

	for id, cfg := range pf.Provider {
		if err := validateProviderID(id); err != nil {
			return nil, err
		}
		// Rule 4: api must be "openai" or "anthropic" exclusively.
		if cfg.API != "openai" && cfg.API != "anthropic" {
			return nil, fmt.Errorf("provider %q: unsupported api dialect %q; must be 'openai' or 'anthropic'", id, cfg.API)
		}
		for mKey, mCfg := range cfg.Models {
			if err := rejectBadBytes(mKey); err != nil {
				return nil, fmt.Errorf("provider %q model %q: %w", id, mKey, err)
			}
			if len(mKey) == 0 || len(mKey) > 256 {
				return nil, fmt.Errorf("provider %q model %q: length must be between 1 and 256 bytes", id, mKey)
			}
			if mCfg.ID != "" {
				if err := rejectBadBytes(mCfg.ID); err != nil {
					return nil, fmt.Errorf("provider %q model %q alias ID %q: %w", id, mKey, mCfg.ID, err)
				}
			}
		}
	}

	return pf.Provider, nil
}

// checkNoLiteralKeys scans JSON tokens to ensure no credential field names exist.
func checkNoLiteralKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		t, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("providers.json syntax: %w", err)
		}
		if str, ok := t.(string); ok {
			lower := strings.ToLower(str)
			switch lower {
			case "apikey", "api_key", "key", "token", "secret", "password", "bearer", "authorization":
				return fmt.Errorf("providers.json: literal key or credential field %q is forbidden; secrets belong in auth.json", str)
			}
			// Also reject credential-like values if any appears
			if strings.HasPrefix(str, "sk-") || strings.HasPrefix(str, "gsk_") || strings.HasPrefix(str, "nvapi-") {
				return errors.New("providers.json: literal API key detected; secrets belong exclusively in auth.json")
			}
		}
	}
	return nil
}

// ValidProviderID reports whether id is a legal provider identifier: 1..32
// bytes matching [a-z0-9-_]. Callers outside this package use it to validate a
// provider name before it reaches the registry.
func ValidProviderID(id string) error { return validateProviderID(id) }

// validateProviderID validates that a provider ID is [a-z0-9-_] and <= 32 bytes.
func validateProviderID(id string) error {
	if err := rejectBadBytes(id); err != nil {
		return fmt.Errorf("provider ID %q: %w", id, err)
	}
	if len(id) == 0 || len(id) > 32 {
		return fmt.Errorf("provider ID %q: length must be between 1 and 32 bytes (got %d)", id, len(id))
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return fmt.Errorf("provider ID %q: invalid character %q; must match [a-z0-9-_]", id, c)
		}
	}
	return nil
}

// rejectBadBytes checks for forbidden ASCII control chars and invalid UTF-8.
func rejectBadBytes(s string) error {
	if !utf8.ValidString(s) {
		return errors.New("value contains invalid UTF-8 bytes")
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b < 0x20 || b == 0x7F {
			return fmt.Errorf("value contains forbidden control character 0x%02X at byte %d", b, i)
		}
	}
	return nil
}

// WriteProvidersFile writes providers configuration to path with strict 0600 permissions.
func WriteProvidersFile(path string, providers map[string]ProviderConfig) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	pf := ProvidersFile{Provider: providers}
	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
