package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var providerKeyNames = map[string]string{
	"anthropic": "ANTHROPIC_API_KEY", "groq": "GROQ_API_KEY",
	"openrouter": "OPENROUTER_API_KEY", "nvidia": "NVIDIA_API_KEY",
}

type V2Config struct {
	Version                int                     `json:"version"`
	Provider               string                  `json:"provider"`
	Model                  string                  `json:"model,omitempty"`
	BaseURL                string                  `json:"base_url,omitempty"`
	Routes                 []V2Route               `json:"routes,omitempty"`
	RouterMode             string                  `json:"router_mode,omitempty"`
	RouterPrestreamTimeout string                  `json:"router_prestream_timeout,omitempty"`
	ProviderTurnTimeout    string                  `json:"provider_turn_timeout,omitempty"`
	Limits                 V2Limits                `json:"limits,omitempty"`
	Credentials            map[string]V2Credential `json:"credentials"`
}

type V2Route struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type V2Credential struct {
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
}

type V2Limits struct {
	Context         int `json:"context,omitempty"`
	MaxTokens       int `json:"max_tokens,omitempty"`
	MaxTokensPerRun int `json:"max_tokens_per_run,omitempty"`
	MaxRead         int `json:"max_read,omitempty"`
}

func V2Path() (string, error) {
	if p := strings.TrimSpace(os.Getenv(V2EnvVar)); p != "" {
		if !filepath.IsAbs(p) {
			return "", fmt.Errorf("%s must be an absolute user-scoped path", V2EnvVar)
		}
		return filepath.Clean(p), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ag", "config.v2.json"), nil
}

// SelectedPath selects one user-scoped configuration. Explicitly selecting
// both versions, or having both default files present, is fatal.
func SelectedPath() (string, int, error) {
	v1, err := Path()
	if err != nil {
		return "", 0, err
	}
	v2, err := V2Path()
	if err != nil {
		return "", 0, err
	}
	explicitV1 := strings.TrimSpace(os.Getenv(EnvVar)) != ""
	explicitV2 := strings.TrimSpace(os.Getenv(V2EnvVar)) != ""
	if explicitV1 && explicitV2 {
		return "", 0, errors.New("Config v1 and Config v2 cannot be selected together")
	}
	v1Present, err := pathPresent(v1)
	if err != nil {
		return "", 0, err
	}
	v2Present, err := pathPresent(v2)
	if err != nil {
		return "", 0, err
	}
	if v1Present && v2Present {
		return "", 0, errors.New("Config v1 and Config v2 files cannot coexist")
	}
	if explicitV2 || v2Present {
		return v2, 2, nil
	}
	return v1, 1, nil
}

func pathPresent(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func loadSelected() (map[string]string, int, error) {
	path, version, err := SelectedPath()
	if err != nil {
		return nil, 0, err
	}
	if version == 2 {
		v, err := ParseV2File(path)
		return v, 2, err
	}
	v, err := ParseFile(path)
	return v, 1, err
}

// ParseSelectedFile parses the selected file without mutating the process-wide
// load cache. It is used by the config diagnostics commands.
func ParseSelectedFile() (map[string]string, int, error) {
	path, version, err := SelectedPath()
	if err != nil {
		return nil, 0, err
	}
	if version == 2 {
		v, err := ParseV2File(path)
		return v, 2, err
	}
	v, err := ParseFile(path)
	return v, 1, err
}

func ParseV2File(path string) (map[string]string, error) {
	data, err := readSecureFile(path, MaxFileBytes)
	if err != nil {
		return nil, err
	}
	var cfg V2Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("Config v2: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("Config v2: trailing JSON content")
	}
	return flattenV2(cfg)
}

func readSecureFile(path string, limit int64) ([]byte, error) {
	f, fi, err := openConfigFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := validateOpenedFile(path, fi); err != nil {
		return nil, err
	}
	if fi.Size() > limit {
		return nil, fmt.Errorf("%s: file exceeds %d bytes", path, limit)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: file exceeds %d bytes", path, limit)
	}
	return data, nil
}

func flattenV2(cfg V2Config) (map[string]string, error) {
	if cfg.Version != 2 {
		return nil, fmt.Errorf("Config v2: version must be 2")
	}
	if _, ok := providerKeyNames[cfg.Provider]; !ok && cfg.Provider != "router" {
		return nil, fmt.Errorf("Config v2: unsupported provider %q", cfg.Provider)
	}
	if cfg.Provider == "router" {
		if cfg.Model != "" || cfg.BaseURL != "" {
			return nil, errors.New("Config v2: router forbids model and base_url")
		}
		if len(cfg.Routes) == 0 {
			return nil, errors.New("Config v2: router requires routes")
		}
	} else if len(cfg.Routes) != 0 || cfg.RouterMode != "" || cfg.RouterPrestreamTimeout != "" {
		return nil, errors.New("Config v2: routes and router settings require provider=router")
	}
	if len(cfg.Routes) > 32 {
		return nil, errors.New("Config v2: routes exceeds 32 entries")
	}
	if cfg.BaseURL != "" {
		if err := ValidateEndpointURL(cfg.BaseURL); err != nil {
			return nil, fmt.Errorf("Config v2 base_url: %w", err)
		}
	}
	out := map[string]string{"NABD_PROVIDER": cfg.Provider}
	if cfg.Model != "" {
		out["NABD_MODEL"] = cfg.Model
	}
	if cfg.BaseURL != "" {
		out["NABD_BASE_URL"] = strings.TrimRight(cfg.BaseURL, "/")
	}
	if cfg.RouterMode != "" {
		if cfg.RouterMode != "fallback" {
			return nil, errors.New("Config v2: router_mode must be fallback")
		}
		out["NABD_ROUTER_MODE"] = cfg.RouterMode
	}
	if cfg.RouterPrestreamTimeout != "" {
		out["NABD_ROUTER_PRESTREAM_TIMEOUT"] = cfg.RouterPrestreamTimeout
	}
	if cfg.ProviderTurnTimeout != "" {
		out["NABD_PROVIDER_TURN_TIMEOUT"] = cfg.ProviderTurnTimeout
	}
	if err := flattenLimits(cfg.Limits, out); err != nil {
		return nil, err
	}
	var routes []string
	needed := map[string]struct{}{}
	if cfg.Provider == "router" {
		for i, route := range cfg.Routes {
			if _, ok := providerKeyNames[route.Provider]; !ok {
				return nil, fmt.Errorf("Config v2: route %d has unsupported provider %q", i, route.Provider)
			}
			if strings.TrimSpace(route.Model) == "" || strings.ContainsAny(route.Model, ",:\r\n") {
				return nil, fmt.Errorf("Config v2: route %d has invalid model", i)
			}
			routes = append(routes, route.Provider+":"+route.Model)
			needed[route.Provider] = struct{}{}
		}
		out["NABD_ROUTES"] = strings.Join(routes, ",")
	} else {
		needed[cfg.Provider] = struct{}{}
	}
	for providerName, cred := range cfg.Credentials {
		keyName, ok := providerKeyNames[providerName]
		if !ok {
			return nil, fmt.Errorf("Config v2: credential for unsupported provider %q", providerName)
		}
		value, err := resolveCredential(providerName, keyName, cred)
		if err != nil {
			return nil, err
		}
		if value != "" {
			out[keyName] = value
		}
	}
	for providerName := range needed {
		if _, ok := cfg.Credentials[providerName]; !ok {
			return nil, fmt.Errorf("Config v2: credential source required for provider %q", providerName)
		}
	}
	return out, nil
}

func flattenLimits(l V2Limits, out map[string]string) error {
	if l.Context != 0 {
		if l.Context <= 8000 {
			return errors.New("Config v2: limits.context must be greater than 8000")
		}
		out["NABD_CTX"] = strconv.Itoa(l.Context)
	}
	if l.MaxTokens != 0 {
		if l.MaxTokens < 128 || l.MaxTokens > 8192 {
			return errors.New("Config v2: limits.max_tokens must be in [128,8192]")
		}
		out["NABD_MAX_TOKENS"] = strconv.Itoa(l.MaxTokens)
	}
	if l.MaxTokensPerRun != 0 {
		if l.MaxTokensPerRun < 1 {
			return errors.New("Config v2: limits.max_tokens_per_run must be positive")
		}
		out["NABD_MAX_TOKENS_PER_RUN"] = strconv.Itoa(l.MaxTokensPerRun)
	}
	if l.MaxRead != 0 {
		if l.MaxRead < 1 || l.MaxRead > MaxFileBytes {
			return nilError("limits.max_read must be in [1,262144]")
		}
		out["NABD_MAX_READ"] = strconv.Itoa(l.MaxRead)
	}
	return nil
}

func nilError(message string) error { return errors.New("Config v2: " + message) }

func resolveCredential(providerName, keyName string, cred V2Credential) (string, error) {
	switch cred.Source {
	case "env":
		if cred.Path != "" {
			return "", fmt.Errorf("Config v2: %s env credential forbids path", providerName)
		}
		return strings.TrimSpace(os.Getenv(keyName)), nil
	case "file":
		if !filepath.IsAbs(cred.Path) {
			return "", fmt.Errorf("Config v2: %s credential path must be absolute", providerName)
		}
		data, err := readSecureFile(filepath.Clean(cred.Path), MaxValueBytes)
		if err != nil {
			return "", fmt.Errorf("Config v2: %s credential file: %w", providerName, err)
		}
		value := strings.TrimSpace(string(data))
		if value == "" || strings.ContainsAny(value, "\r\n") {
			return "", fmt.Errorf("Config v2: %s credential file must contain one non-empty line", providerName)
		}
		return value, nil
	case "command":
		return "", fmt.Errorf("Config v2: %s credential source command is forbidden", providerName)
	default:
		return "", fmt.Errorf("Config v2: %s credential source must be env or file", providerName)
	}
}

// ValidateEndpointURL rejects endpoint shapes and literal addresses that can
// target local services. DNS results are checked again by provider transports.
func ValidateEndpointURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "https" || u.Hostname() == "" {
		return errors.New("HTTPS URL with a host is required")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("userinfo, query and fragment are forbidden")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return errors.New("local hostnames are forbidden")
	}
	if ip := net.ParseIP(host); ip != nil && !PublicIP(ip) {
		return errors.New("non-public IP addresses are forbidden")
	}
	return nil
}

func PublicIP(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsUnspecified() && !ip.IsMulticast()
}
