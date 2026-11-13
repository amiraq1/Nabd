// Package audit appends an immutable, hash-only record of tool activity to
// .nabd/audit.log. File contents and arguments are never written; only hashes
// and short workspace-relative details are stored.
package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

const (
	dir      = ".nabd"
	fileName = ".nabd/audit.log"
)

// Event is one JSON line in the audit log.
type Event struct {
	Time         time.Time `json:"time"`
	Event        string    `json:"event"`
	Tool         string    `json:"tool,omitempty"`
	ArgsSHA256   string    `json:"args_sha256,omitempty"`
	ResultSHA256 string    `json:"result_sha256,omitempty"`
	Detail       string    `json:"detail,omitempty"`
	Reason       string    `json:"reason,omitempty"`
}

// Log appends events inside an already-jailed workspace root.
type Log struct {
	root *os.Root
	mu   sync.Mutex
	now  func() time.Time
}

// Open binds a log to root. Nothing is created until the first Write, so a
// read-only workspace can still be used for reading.
func Open(root *os.Root) (*Log, error) {
	if root == nil {
		return nil, errors.New("audit: nil root")
	}
	return &Log{root: root, now: time.Now}, nil
}

// Write appends one event and fsyncs it.
func (l *Log) Write(event Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if event.Time.IsZero() {
		event.Time = l.now().UTC()
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("audit: encode event: %w", err)
	}
	data = append(data, '\n')

	if err := l.root.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("audit: create directory: %w", err)
	}
	f, err := l.root.OpenFile(fileName, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("audit: open log: %w", err)
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("audit: append event: %w", err)
	}
	return nil
}
