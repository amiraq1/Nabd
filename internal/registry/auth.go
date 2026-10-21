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

	for id, cfg := range af {
		if err := validateProviderID(id); err != nil {
			return nil, redactError(fmt.Errorf("auth provider ID: %w", err))
		}
		if cfg.Type != "" && cfg.Type != "api" {
			return nil, redactError(fmt.Errorf("auth provider %q: unsupported auth type %q (expected 'api')", id, cfg.Type))
		}
	}
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
//
// The write is atomic and symlink-safe:
//   - Data goes to a randomly-named temp file in the same directory, created
//     with O_CREAT|O_EXCL, so a pre-planted file or symlink at the temp name
//     fails closed instead of being followed or truncated. An fstat guard
//     confirms the open descriptor is still the regular file we created.
//   - Permissions are tightened to 0600 on the open descriptor, so a hostile
//     umask cannot widen the file between creation and chmod.
//   - Content is fsynced before rename; rename(2) then atomically swaps the
//     destination without ever following a symlink at path.
//   - The directory is fsynced afterwards on a best-effort basis so the
//     rename itself survives a crash (ignored where directory sync is
//     unsupported).
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

	tmpf, err := os.CreateTemp(dir, ".auth-*.tmp")
	if err != nil {
		return redactError(err)
	}
	tmpName := tmpf.Name()
	// On any failure the temp file is removed; on success the rename below
	// consumes it.
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmpf.Chmod(0o600); err != nil {
		_ = tmpf.Close()
		return redactError(err)
	}
	if st, err := tmpf.Stat(); err != nil {
		_ = tmpf.Close()
		return redactError(err)
	} else if !st.Mode().IsRegular() {
		_ = tmpf.Close()
		return redactError(errors.New("auth: temp file is not a regular file"))
	}
	if _, err := tmpf.Write(data); err != nil {
		_ = tmpf.Close()
		return redactError(err)
	}
	if err := tmpf.Sync(); err != nil {
		_ = tmpf.Close()
		return redactError(err)
	}
	if err := tmpf.Close(); err != nil {
		return redactError(err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return redactError(err)
	}
	removeTmp = false
	if dirf, err := os.Open(dir); err == nil {
		_ = dirf.Sync()
		_ = dirf.Close()
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
