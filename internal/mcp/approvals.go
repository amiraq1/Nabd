package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ExecIdentity binds an approval to the exact executable configuration:
// the resolved absolute path, full argv, sorted env var names, and the
// SHA-256 of the executable's content at approval time. Any change
// invalidates the approval (ADR-0003 §6, MCP-SEC-06/07).
type ExecIdentity struct {
	Path         string   `json:"path"`
	Args         []string `json:"args"`
	EnvNames     []string `json:"env_names"`
	BinarySHA256 string   `json:"binary_sha256"`
}

// Approval is one recorded first-approval for a (server, tool) binding.
type Approval struct {
	Server         string       `json:"server"`
	Tool           string       `json:"tool"`
	Fingerprint    string       `json:"fingerprint"`
	Classification string       `json:"classification"`
	Exec           ExecIdentity `json:"exec"`
	ApprovedAt     time.Time    `json:"approved_at"`
}

// ComputeExecIdentity snapshots the executable identity for approval binding.
func ComputeExecIdentity(execPath string, args, envNames []string) (ExecIdentity, error) {
	content, err := os.ReadFile(execPath)
	if err != nil {
		return ExecIdentity{}, fmt.Errorf("mcp: hash executable: %w", err)
	}
	sum := sha256.Sum256(content)
	names := append([]string(nil), envNames...)
	sort.Strings(names)
	argv := append([]string(nil), args...)
	return ExecIdentity{
		Path:         execPath,
		Args:         argv,
		EnvNames:     names,
		BinarySHA256: hex.EncodeToString(sum[:]),
	}, nil
}

// DefaultApprovalsPath is the only location approvals are stored.
func DefaultApprovalsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".ag", "mcp-approvals.json")
}

// Store is a 0600 JSON approval store: map["server\x00tool"]Approval.
type Store struct {
	path      string
	mu        sync.Mutex
	approvals map[string]Approval
}

// OpenStore loads the approval store, or starts empty if absent.
// A corrupt file fails closed (it is never silently ignored).
func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, approvals: map[string]Approval{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, fmt.Errorf("mcp: read approvals: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f struct {
		Approvals map[string]Approval `json:"approvals"`
	}
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("mcp: parse approvals: %w", err)
	}
	if dec.More() {
		return nil, errors.New("mcp: trailing content in approvals file")
	}
	if f.Approvals != nil {
		s.approvals = f.Approvals
	}
	return s, nil
}

func approvalKey(server, tool string) string { return server + "\x00" + tool }

// Check reports whether a valid approval exists for this exact binding:
// same tool fingerprint AND same executable identity. Anything else fails
// closed and requires re-approval.
func (s *Store) Check(server, tool, fingerprint string, exec ExecIdentity) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.approvals[approvalKey(server, tool)]
	if !ok {
		return false
	}
	if a.Fingerprint != fingerprint {
		return false
	}
	return execIdentEqual(a.Exec, exec)
}

func execIdentEqual(a, b ExecIdentity) bool {
	if a.Path != b.Path || a.BinarySHA256 != b.BinarySHA256 {
		return false
	}
	if len(a.Args) != len(b.Args) || len(a.EnvNames) != len(b.EnvNames) {
		return false
	}
	for i := range a.Args {
		if a.Args[i] != b.Args[i] {
			return false
		}
	}
	for i := range a.EnvNames {
		if a.EnvNames[i] != b.EnvNames[i] {
			return false
		}
	}
	return true
}

// Record stores an approval, persisting the file with mode 0600.
func (s *Store) Record(a Approval) error {
	if a.Server == "" || a.Tool == "" || a.Fingerprint == "" {
		return errors.New("mcp: incomplete approval")
	}
	if a.ApprovedAt.IsZero() {
		a.ApprovedAt = time.Now().UTC()
	}
	s.mu.Lock()
	s.approvals[approvalKey(a.Server, a.Tool)] = a
	data, err := json.Marshal(struct {
		Approvals map[string]Approval `json:"approvals"`
	}{Approvals: s.approvals})
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return os.WriteFile(s.path, data, 0o600)
}
