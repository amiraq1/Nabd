package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"nabd/internal/redact"
)

// AuthConfig stores credentials for a single provider.
type AuthConfig struct {
	Type string `json:"type"`
	Key  string `json:"key"`
}

// AuthFile maps provider identifiers to their credential configurations.
type AuthFile map[string]AuthConfig

// ParseAuthData decodes and strictly validates auth configuration from bytes.
func ParseAuthData(data []byte) (AuthFile, error) {
	var af AuthFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&af); err != nil {
		return nil, redactError(fmt.Errorf("auth: %w", err))
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("auth: trailing JSON content")
	}

	keys := make([]string, 0, len(af))
	for id, cfg := range af {
		if err := validateProviderID(id); err != nil {
			return nil, redactError(fmt.Errorf("auth provider ID: %w", err))
		}
		if cfg.Type != "" && cfg.Type != "api" {
			return nil, redactError(fmt.Errorf("auth provider %q: unsupported auth type %q (expected 'api')", id, cfg.Type))
		}
		if cfg.Key != "" {
			keys = append(keys, cfg.Key)
		}
	}
	_ = keys
	return af, nil
}

// ParseAuthFile securely opens, validates permissions (0600), and parses auth.json.
// Returns an empty map if the file does not exist.
func ParseAuthFile(path string) (AuthFile, error) {
	data, err := readSecureFile(path, MaxFileBytes)
	if errors.Is(err, os.ErrNotExist) {
		return AuthFile{}, nil
	}
	if err != nil {
		return nil, redactError(err)
	}
	return ParseAuthData(data)
}

// WriteAuthFile writes credentials to path with strict 0600 permissions.
func WriteAuthFile(path string, af AuthFile) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return redactError(err)
	}
	data, err := json.MarshalIndent(af, "", "  ")
	if err != nil {
		return redactError(err)
	}
	data = append(data, '\n')
	// Write with 0600 permissions.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return redactError(err)
	}
	// Explicit chmod in case umask widened the file.
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return redactError(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return redactError(err)
	}
	return nil
}

// redactError ensures no credential patterns leak in an error string.
func redactError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(redact.Redact(err.Error()))
}
