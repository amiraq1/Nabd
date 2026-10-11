package mcp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Journal records MCP events without raw arguments or raw outputs
// (MCP-SEC-13). Argument privacy uses HMAC-SHA256 with a local key — a plain
// hash would be guessable for short arguments.
type Journal struct {
	path string
	key  []byte
	mu   sync.Mutex
}

// JournalEvent is one journaled MCP event.
type JournalEvent struct {
	Time            string `json:"time"`
	Kind            string `json:"kind"` // "server_start", "call", "approval", "server_timeout"
	Server          string `json:"server"`
	ProtocolVersion string `json:"protocol_version,omitempty"`
	Tool            string `json:"tool,omitempty"`
	Decision        string `json:"decision,omitempty"` // "approved", "denied"
	DurationMs      int64  `json:"duration_ms,omitempty"`
	ArgsHMAC        string `json:"args_hmac,omitempty"` // HMAC-SHA256, never raw args
	OutputBytes     int    `json:"output_bytes,omitempty"`
	Status          string `json:"status,omitempty"` // "ok", "error", "timeout"
	// AbortedCalls counts in-flight calls killed with the server on a
	// timeout (MCP-SEC-11/12). Set only on "server_timeout" events.
	AbortedCalls int `json:"aborted_calls,omitempty"`
}

// DefaultJournalKeyPath is the only location of the journal HMAC key.
// It matches the production path used by cmd/ag wiring.
func DefaultJournalKeyPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".ag", "mcp", "journal.key")
}

// DefaultJournalPath is the only location of the MCP journal.
// It matches the production path used by cmd/ag wiring.
func DefaultJournalPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".ag", "mcp", "journal.jsonl")
}

// loadOrCreateKey loads the 0600 HMAC key, generating it once if absent.
// Reads and writes are symlink/owner/permission-hardened.
func loadOrCreateKey(path string) ([]byte, error) {
	if raw, err := readSecureFile(path, 64); err == nil {
		if len(raw) != 32 {
			return nil, errors.New("mcp: journal key has wrong length")
		}
		return raw, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := writeSecureFile(path, key); err != nil {
		return nil, err
	}
	return key, nil
}

// OpenJournal opens (or creates) the journal at path with the HMAC key at
// keyPath. Empty paths disable the journal (returns nil, nil) — used in tests.
func OpenJournal(path, keyPath string) (*Journal, error) {
	if path == "" || keyPath == "" {
		return nil, nil
	}
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return nil, fmt.Errorf("mcp: journal key: %w", err)
	}
	return &Journal{path: path, key: key}, nil
}

// HMACArgs returns the hex HMAC-SHA256 of raw arguments.
func (j *Journal) HMACArgs(raw json.RawMessage) string {
	mac := hmac.New(sha256.New, j.key)
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}

// Append records one event as a JSON line.
func (j *Journal) Append(ev JournalEvent) error {
	if j == nil {
		return nil
	}
	ev.Time = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	// Symlink/owner/permission-hardened append.
	f, err := openSecureAppend(j.path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}
