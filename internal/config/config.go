// Package config reads Nabd's user-scoped configuration.
// Provider selection, credentials, models and base URLs are accepted only
// from explicit user paths or ~/.ag. Project files are never consulted.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	EnvVar        = "NABD_CONFIG"
	V2EnvVar      = "NABD_CONFIG_V2"
	MaxFileBytes  = 256 << 10
	MaxLineBytes  = 64 << 10
	MaxKeys       = 256
	MaxKeyBytes   = 128
	MaxValueBytes = 64 << 10
)

var knownV1Keys = map[string]struct{}{
	"ANTHROPIC_API_KEY": {}, "GROQ_API_KEY": {}, "OPENROUTER_API_KEY": {}, "NVIDIA_API_KEY": {},
	"NABD_PROVIDER": {}, "NABD_MODEL": {}, "NABD_BASE_URL": {}, "NABD_ROUTES": {},
	"NABD_ROUTER_MODE": {}, "NABD_ROUTER_PRESTREAM_TIMEOUT": {}, "NABD_PROVIDER_TURN_TIMEOUT": {},
	"NABD_CTX": {}, "NABD_MAX_TOKENS": {}, "NABD_MAX_TOKENS_PER_RUN": {}, "NABD_MAX_READ": {},
}

var (
	once          sync.Once
	values        map[string]string
	loadErr       error
	activeVersion int
)

func Path() (string, error) {
	if p := strings.TrimSpace(os.Getenv(EnvVar)); p != "" {
		if !filepath.IsAbs(p) {
			return "", fmt.Errorf("%s must be an absolute user-scoped path", EnvVar)
		}
		return filepath.Clean(p), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ag", "config"), nil
}

func Load() error {
	once.Do(func() { values, activeVersion, loadErr = loadSelected() })
	return loadErr
}

// Version returns the selected configuration version after loading.
func Version() int {
	if Load() != nil {
		return 0
	}
	return activeVersion
}

// Get fails closed: a present-but-invalid config never falls back to an
// environment credential. Config v2 additionally disables implicit environment
// fallback; every credential source must be declared in the v2 document.
func Get(key string) string {
	if Load() != nil {
		return ""
	}
	if v, ok := values[key]; ok && v != "" {
		return v
	}
	if activeVersion == 2 {
		return ""
	}
	return strings.TrimSpace(os.Getenv(key))
}

func GetOr(key, fallback string) string {
	if v := Get(key); v != "" {
		return v
	}
	return fallback
}

func Has(key string) bool { return Get(key) != "" }

type Conflict struct{ Key string }

func Conflicts() []Conflict {
	if Load() != nil || activeVersion == 2 {
		return nil
	}
	var out []Conflict
	for k, fv := range values {
		fv = strings.TrimSpace(fv)
		ev := strings.TrimSpace(os.Getenv(k))
		if fv != "" && ev != "" && ev != fv {
			out = append(out, Conflict{Key: k})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Warnings reports unknown v1 keys by name only. Values are never included.
func Warnings(v map[string]string) []string {
	out := make([]string, 0)
	for k := range v {
		if _, ok := knownV1Keys[k]; !ok {
			out = append(out, fmt.Sprintf("unknown v1 key %s", k))
		}
	}
	sort.Strings(out)
	return out
}

func ResetForTest() {
	once = sync.Once{}
	values = nil
	loadErr = nil
	activeVersion = 0
}

// ParseFile securely opens and validates a regular user-owned 0600 v1 file.
func ParseFile(p string) (map[string]string, error) {
	f, fi, err := openConfigFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := validateOpenedFile(p, fi); err != nil {
		return nil, err
	}
	return Parse(io.LimitReader(f, MaxFileBytes+1))
}

func validateOpenedFile(p string, fi os.FileInfo) error {
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: regular file required", p)
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("%s: permissions %04o are open to others; run chmod 600 %s", p, mode, p)
	}
	if err := checkOwner(p, fi); err != nil {
		return err
	}
	if fi.Size() > MaxFileBytes {
		return fmt.Errorf("%s: config exceeds %d bytes", p, MaxFileBytes)
	}
	return nil
}

func checkOwner(p string, fi os.FileInfo) error { return checkOwnerPlatform(p, fi) }

func Parse(r interface{ Read([]byte) (int, error) }) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), MaxLineBytes+1)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE", n)
		}
		k = strings.TrimSpace(k)
		if k == "" || len(k) > MaxKeyBytes || strings.ContainsAny(k, " \t\r\n") {
			return nil, fmt.Errorf("line %d: invalid key", n)
		}
		if _, duplicate := out[k]; duplicate {
			return nil, fmt.Errorf("line %d: duplicate key %s", n, k)
		}
		v = strings.TrimSpace(v)
		switch {
		case len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0]:
			v = v[1 : len(v)-1]
		default:
			if i := strings.Index(v, " #"); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
		}
		if len(v) > MaxValueBytes {
			return nil, fmt.Errorf("line %d: value for %s exceeds %d bytes", n, k, MaxValueBytes)
		}
		out[k] = v
		if len(out) > MaxKeys {
			return nil, fmt.Errorf("config exceeds %d keys", MaxKeys)
		}
	}
	if err := sc.Err(); err != nil {
		if strings.Contains(err.Error(), "token too long") {
			return nil, fmt.Errorf("config line exceeds %d bytes", MaxLineBytes)
		}
		return nil, err
	}
	return out, nil
}
