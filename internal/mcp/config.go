package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Size and count caps (ADR-0003 §12, MCP-SEC-16–20).
const (
	MaxConfigBytes    int64 = 256 * 1024
	MaxServers              = 16
	MaxToolsPerServer       = 256
	MaxEnvEntries           = 64
	MaxArgs                 = 64
	MaxArgLen               = 4096
	MaxEnvNameLen           = 128

	DefaultMaxParallelCalls = 4
	MaxParallelCallsCap     = 16
	DefaultCallTimeoutMs    = 120000
	MaxCallTimeoutMs        = 600000
)

// Classification is the LOCAL permission class asserted by the user for a
// server tool. It is a claim, never a verified property (ADR-0003 §5).
type Classification string

const (
	ClassReadOnly Classification = "read-only"
	ClassWrite    Classification = "write"
	ClassExecute  Classification = "execute"
)

var (
	serverIDPattern = regexp.MustCompile(`^[a-z0-9-_]{1,32}$`)
	toolNamePattern = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)
	envNamePattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// envRefPattern: values must be EXACTLY ${VAR}; embedded or partial
	// references are rejected so secrets can never hide in larger strings.
	envRefPattern = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*\}$`)
)

// ServerConfig is one stdio MCP server definition from ~/.ag/mcp.json.
type ServerConfig struct {
	Enabled          bool                      `json:"enabled"`
	Transport        string                    `json:"transport"`
	Command          string                    `json:"command"`
	Args             []string                  `json:"args"`
	Env              map[string]string         `json:"env"`
	Tools            map[string]Classification `json:"tools"`
	MaxParallelCalls int                       `json:"max_parallel_calls"`
	CallTimeoutMs    int64                     `json:"call_timeout_ms"`
}

// Config is the whole ~/.ag/mcp.json document.
type Config struct {
	Servers map[string]ServerConfig `json:"servers"`
}

// DefaultPath returns the only location MCP configuration is ever read from.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".ag", "mcp.json")
}

// isAllowedEnvName reports whether name may appear in the base environment of
// a spawned server (MCP-SEC-01). Exact allowlist: PATH, TERM, LANG and the
// LC_* locale group. Declared ${VAR} secrets from the config are resolved
// separately and are NOT covered by this predicate.
func isAllowedEnvName(name string) bool {
	switch name {
	case "PATH", "TERM", "LANG":
		return true
	}
	return strings.HasPrefix(name, "LC_") && len(name) > len("LC_") && envNamePattern.MatchString(name)
}

// isReservedEnvName reports names the config may not declare because the
// spawner controls them: PATH (sanitizePath), HOME (isolated temp dir), and
// the dynamic-loader prefixes LD_*/DYLD_* which subvert process loading.
func isReservedEnvName(k string) bool {
	if k == "PATH" || k == "HOME" {
		return true
	}
	return strings.HasPrefix(k, "LD_") || strings.HasPrefix(k, "DYLD_")
}

// Load loads MCP configuration from the single supported location,
// DefaultPath(), and validates it. projectRoot is the session project root;
// a config resolving inside it is refused (MCP-SEC-03). A missing file means
// "not configured" and yields an empty Config. Pinning the location here
// (fail-closed) is what makes the "only ~/.ag/mcp.json" claim true:
// callers cannot point the loader anywhere else.
//
// Edge case: if the project root IS the home directory itself, the config
// always resolves inside the tree and is refused. That is fail-closed and
// intended; run from a subdirectory to use MCP.
func Load(projectRoot string) (*Config, error) {
	path := DefaultPath()
	if path == "" {
		return nil, errors.New("mcp config: cannot determine home directory")
	}
	return loadConfig(path, projectRoot)
}

// loadConfig is the testable core of Load. Production callers
// must go through Load so the location pin cannot be bypassed.
func loadConfig(path, projectRoot string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("mcp config: %w", err)
	}
	if insideProjectTree(dereference(abs), projectRoot) {
		return nil, fmt.Errorf("mcp config: %s is inside the project tree; MCP configuration loads from ~/.ag/mcp.json only (if the project root is the home directory itself, the config can never load -- run from a subdirectory)", path)
	}
	data, err := readSecureFile(abs, MaxConfigBytes)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("mcp config: %w", err)
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return nil, err
	}
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("mcp config: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("mcp config: trailing JSON content")
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// dereference resolves as many symlink components as exist. For a missing
// file it resolves the parent directory instead, so the "not configured"
// case still gets a meaningful tree check.
func dereference(p string) string {
	if rp, err := filepath.EvalSymlinks(p); err == nil {
		return rp
	}
	parent := filepath.Dir(p)
	if rp, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.Join(rp, filepath.Base(p))
	}
	return p
}

// insideProjectTree reports whether abs resolves inside root. The criterion
// is location, not file name (ADR-0003 §5.1). Both sides are symlink-resolved:
// O_NOFOLLOW guards only the final path component, so a symlinked parent
// directory would otherwise defeat the textual comparison.
func insideProjectTree(abs, root string) bool {
	if root == "" {
		return false
	}
	rAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	rAbs = dereference(rAbs)
	abs = dereference(filepath.Clean(abs))
	if abs == rAbs {
		return true
	}
	rel, err := filepath.Rel(rAbs, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (c *Config) validate() error {
	if len(c.Servers) > MaxServers {
		return fmt.Errorf("mcp config: %d servers exceed cap %d", len(c.Servers), MaxServers)
	}
	for id := range c.Servers {
		if !serverIDPattern.MatchString(id) {
			return fmt.Errorf("mcp config: server id %q must match [a-z0-9-_]{1,32}", id)
		}
		srv := c.Servers[id]
		if err := srv.validate(); err != nil {
			return fmt.Errorf("mcp config: server %q: %w", id, err)
		}
		c.Servers[id] = srv // persist applied defaults
	}
	return nil
}

func (s *ServerConfig) validate() error {
	if s.Transport != "stdio" {
		return fmt.Errorf("transport %q rejected: v1 supports stdio only", s.Transport)
	}
	if s.Command == "" || !filepath.IsAbs(s.Command) {
		return fmt.Errorf("command must be an absolute path")
	}
	if len(s.Args) > MaxArgs {
		return fmt.Errorf("%d args exceed cap %d", len(s.Args), MaxArgs)
	}
	for _, a := range s.Args {
		if len(a) > MaxArgLen {
			return fmt.Errorf("arg exceeds %d bytes", MaxArgLen)
		}
	}
	if len(s.Env) > MaxEnvEntries {
		return fmt.Errorf("%d env entries exceed cap %d", len(s.Env), MaxEnvEntries)
	}
	for k, v := range s.Env {
		if len(k) > MaxEnvNameLen || !envNamePattern.MatchString(k) {
			return fmt.Errorf("env name %q invalid", k)
		}
		if isReservedEnvName(k) {
			return fmt.Errorf("env %q is reserved: the spawner controls it", k)
		}
		if !envRefPattern.MatchString(v) {
			return fmt.Errorf("env %q: value must be exactly ${VAR}; explicit secrets rejected", k)
		}
	}
	if len(s.Tools) > MaxToolsPerServer {
		return fmt.Errorf("%d tools exceed cap %d", len(s.Tools), MaxToolsPerServer)
	}
	for name, class := range s.Tools {
		if !toolNamePattern.MatchString(name) {
			return fmt.Errorf("tool name %q must match [a-z0-9_-]{1,64}", name)
		}
		switch class {
		case ClassReadOnly, ClassWrite, ClassExecute:
		default:
			return fmt.Errorf("tool %q: classification %q invalid (want read-only|write|execute)", name, class)
		}
	}
	if s.MaxParallelCalls == 0 {
		s.MaxParallelCalls = DefaultMaxParallelCalls
	} else if s.MaxParallelCalls < 1 || s.MaxParallelCalls > MaxParallelCallsCap {
		return fmt.Errorf("max_parallel_calls %d out of range [1,%d]", s.MaxParallelCalls, MaxParallelCallsCap)
	}
	if s.CallTimeoutMs == 0 {
		s.CallTimeoutMs = DefaultCallTimeoutMs
	} else if s.CallTimeoutMs < 0 || s.CallTimeoutMs > MaxCallTimeoutMs {
		return fmt.Errorf("call_timeout_ms %d out of range [0,%d]", s.CallTimeoutMs, MaxCallTimeoutMs)
	}
	return nil
}

// duplicateKeyError is returned when a JSON object repeats a key.
// encoding/json silently takes the last occurrence, which would make
// e.g. "tool":"read-only" followed by "tool":"execute" ambiguous.
type duplicateKeyError struct{ key string }

func (e *duplicateKeyError) Error() string {
	return fmt.Sprintf("mcp config: duplicate key %q", e.key)
}

// rejectDuplicateKeys scans the document for repeated object keys.
// Malformed JSON is ignored here; the typed decode below reports it.
func rejectDuplicateKeys(data []byte) error {
	if err := checkJSONValue(json.NewDecoder(bytes.NewReader(data))); err != nil {
		var dup *duplicateKeyError
		if errors.As(err, &dup) {
			return dup
		}
	}
	return nil
}

func checkJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '{':
		seen := make(map[string]struct{})
		for dec.More() {
			ktok, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := ktok.(string)
			if !ok {
				return fmt.Errorf("invalid object key")
			}
			if _, dup := seen[key]; dup {
				return &duplicateKeyError{key: key}
			}
			seen[key] = struct{}{}
			if err := checkJSONValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	case '[':
		for dec.More() {
			if err := checkJSONValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	}
	return nil
}
