package journal

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

var (
	ErrStoreClosed        = errors.New("store is closed")
	ErrCorruptedRecord    = errors.New("corrupted record in journal")
	ErrInvalidSequence    = errors.New("event seq violates sequence monotonicity")
	ErrInvalidEventParent = errors.New("event parent is invalid")
)

type Store interface {
	Append(ctx context.Context, e Event) (seq int64, err error)
	ReadAll(ctx context.Context) ([]Event, error)
	LastSeq() int64
	Close() error
}

type SyncMode int

const (
	SyncAlways SyncMode = iota
	SyncBatch
	SyncNone
)

type Options struct {
	Path       string
	SyncMode   SyncMode
	FailInject FailInjectFunc
}

type FailInjectFunc func(op string) error

type fileStore struct {
	mu      sync.Mutex
	file    *os.File
	lastSeq int64
	closed  bool
	opts    Options
}

// OpenStore opens or creates a JSONL journal store at the specified path.
// It verifies existing records, cleanly recovers from any truncated tail at EOF,
// and ensures seq numbers continue monotonically from the last valid record.
func OpenStore(opts Options) (Store, error) {
	if opts.Path == "" {
		return nil, errors.New("store path cannot be empty")
	}

	dir := filepath.Dir(opts.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating store directory: %w", err)
	}

	f, err := os.OpenFile(opts.Path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("opening store file: %w", err)
	}

	s := &fileStore{
		file: f,
		opts: opts,
	}

	if err := s.recoverAndScan(); err != nil {
		_ = f.Close()
		return nil, err
	}

	return s, nil
}

// recoverAndScan reads the file from the start to establish lastSeq.
// It discards a partial/unclosed tail at EOF, but rejects corruption in the middle.
func (s *fileStore) recoverAndScan() error {
	info, err := s.file.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		s.lastSeq = 0
		return nil
	}

	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return err
	}

	reader := bufio.NewReader(s.file)
	var (
		lastValidOffset int64
		currentOffset   int64
		lastSeq         int64
		lineCount       int
	)

	for {
		line, isPrefix, readErr := reader.ReadLine()
		if isPrefix {
			return fmt.Errorf("%w: line too long at offset %d", ErrCorruptedRecord, currentOffset)
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				// If there were dangling bytes without trailing newline at EOF, that's a truncated tail.
				if len(line) > 0 {
					// Discard truncated tail by truncating back to lastValidOffset
					if err := s.file.Truncate(lastValidOffset); err != nil {
						return fmt.Errorf("truncating incomplete tail: %w", err)
					}
				}
				break
			}
			return readErr
		}

		// Calculate bytes read for this line including the newline
		// ReadLine strips \r\n, so we account for the original slice length + delimiter
		rawLineLen := int64(len(line)) + 1 // +1 for '\n'
		// Check if carriage return was present
		testPos := currentOffset + rawLineLen
		if testPos <= info.Size() {
			// standard \n
		}

		clean := bytes.TrimSpace(line)
		if len(clean) == 0 {
			currentOffset += rawLineLen
			continue
		}

		ev, decErr := DecodeEvent(clean)
		if decErr != nil {
			// Check if this error is at the very tail of the file (no further valid lines exist).
			// If we are at EOF, treat as truncated tail and truncate.
			peekBytes, peekErr := reader.Peek(1)
			if (peekErr != nil && errors.Is(peekErr, io.EOF)) || len(peekBytes) == 0 {
				if err := s.file.Truncate(lastValidOffset); err != nil {
					return fmt.Errorf("truncating corrupted tail: %w", err)
				}
				break
			}
			// Middle corruption
			return fmt.Errorf("%w: invalid JSON at line %d (offset %d): %v", ErrCorruptedRecord, lineCount+1, currentOffset, decErr)
		}

		if lineCount == 0 && ev.Seq != 1 {
			return fmt.Errorf("%w: first event seq must be 1, got %d", ErrInvalidSequence, ev.Seq)
		}
		if lineCount > 0 && ev.Seq != lastSeq+1 {
			return fmt.Errorf("%w: expected seq %d, got %d", ErrInvalidSequence, lastSeq+1, ev.Seq)
		}

		lastSeq = ev.Seq
		lineCount++

		// Advance to next offset
		pos, seekErr := s.file.Seek(0, io.SeekCurrent)
		if seekErr == nil {
			// Account for reader buffer
			lastValidOffset = pos - int64(reader.Buffered())
		} else {
			lastValidOffset += rawLineLen
		}
		currentOffset = lastValidOffset
	}

	s.lastSeq = lastSeq
	_, err = s.file.Seek(0, io.SeekEnd)
	return err
}

func (s *fileStore) Append(ctx context.Context, e Event) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return 0, ErrStoreClosed
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	// Seq assignment / sequence monotonicity validation
	targetSeq := s.lastSeq + 1
	if e.Seq == 0 {
		e.Seq = targetSeq
	} else if e.Seq != targetSeq {
		return 0, fmt.Errorf("%w: expected %d, got %d", ErrInvalidSequence, targetSeq, e.Seq)
	}

	// Parent validation
	if e.Seq == 1 {
		if e.Parent != 0 {
			return 0, fmt.Errorf("%w: root event must have parent 0", ErrInvalidEventParent)
		}
		if e.Kind != SessionStart {
			return 0, errors.New("first event must be session_start")
		}
	} else {
		if e.Parent <= 0 || e.Parent >= e.Seq {
			return 0, fmt.Errorf("%w: event seq %d has invalid parent %d", ErrInvalidEventParent, e.Seq, e.Parent)
		}
	}

	// Fault injection before write
	if s.opts.FailInject != nil {
		if err := s.opts.FailInject("before_write"); err != nil {
			return 0, err
		}
	}

	// Encode to line
	encoded, err := EncodeEvent(e)
	if err != nil {
		return 0, fmt.Errorf("encoding event: %w", err)
	}

	var buf bytes.Buffer
	buf.Write(encoded)
	buf.WriteByte('\n')

	if _, err := s.file.Write(buf.Bytes()); err != nil {
		return 0, fmt.Errorf("writing event to disk: %w", err)
	}

	// Fault injection before sync
	if s.opts.FailInject != nil {
		if err := s.opts.FailInject("before_sync"); err != nil {
			return 0, err
		}
	}

	// Fsync policy
	if s.opts.SyncMode == SyncAlways {
		if err := s.file.Sync(); err != nil {
			return 0, fmt.Errorf("syncing file to disk: %w", err)
		}
	}

	s.lastSeq = e.Seq
	return e.Seq, nil
}

func (s *fileStore) ReadAll(ctx context.Context) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrStoreClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Sync any pending writes before reading
	if s.opts.SyncMode == SyncBatch {
		_ = s.file.Sync()
	}

	info, err := s.file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() == 0 {
		return []Event{}, nil
	}

	// Read fresh from disk
	f, err := os.Open(s.opts.Path)
	if err != nil {
		return nil, fmt.Errorf("opening file for read: %w", err)
	}
	defer f.Close()

	var events []Event
	reader := bufio.NewReader(f)
	var (
		lastValidOffset int64
		currentOffset   int64
		lastSeq         int64
		lineCount       int
	)

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		line, isPrefix, readErr := reader.ReadLine()
		if isPrefix {
			return nil, fmt.Errorf("%w: line too long at offset %d", ErrCorruptedRecord, currentOffset)
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				// Clean EOF
				break
			}
			return nil, readErr
		}

		clean := bytes.TrimSpace(line)
		if len(clean) == 0 {
			continue
		}

		ev, decErr := DecodeEvent(clean)
		if decErr != nil {
			// Check if at EOF (truncated tail)
			peekBytes, peekErr := reader.Peek(1)
			if (peekErr != nil && errors.Is(peekErr, io.EOF)) || len(peekBytes) == 0 {
				// Ignore truncated tail
				break
			}
			return nil, fmt.Errorf("%w: at line %d (offset %d): %v", ErrCorruptedRecord, lineCount+1, currentOffset, decErr)
		}

		if lineCount == 0 && ev.Seq != 1 {
			return nil, fmt.Errorf("%w: first event seq must be 1, got %d", ErrInvalidSequence, ev.Seq)
		}
		if lineCount > 0 && ev.Seq != lastSeq+1 {
			return nil, fmt.Errorf("%w: expected seq %d, got %d", ErrInvalidSequence, lastSeq+1, ev.Seq)
		}

		lastSeq = ev.Seq
		events = append(events, ev)
		lineCount++

		pos, _ := f.Seek(0, io.SeekCurrent)
		lastValidOffset = pos - int64(reader.Buffered())
		currentOffset = lastValidOffset
	}

	return events, nil
}

func (s *fileStore) LastSeq() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSeq
}

func (s *fileStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	var syncErr error
	if s.opts.SyncMode != SyncNone {
		syncErr = s.file.Sync()
	}

	closeErr := s.file.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
