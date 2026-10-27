package journal

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalidEventTime    = errors.New("event time must be non-zero and UTC")
	ErrInvalidEventKind    = errors.New("event kind must not be empty")
	ErrInvalidNoticeStatus = errors.New("invalid notice status for code")
	ErrInvalidChangeAction = errors.New("invalid change action")
	ErrMissingEventData    = errors.New("event data cannot be empty")
)

// EncodeEvent encodes an Event into a single JSON line without HTML escaping or trailing newline.
func EncodeEvent(e Event) ([]byte, error) {
	if e.Time.IsZero() || e.Time.Location() != time.UTC {
		return nil, ErrInvalidEventTime
	}
	if e.Kind == "" {
		return nil, ErrInvalidEventKind
	}
	if len(e.Data) == 0 {
		return nil, ErrMissingEventData
	}

	// Validate known payload constraints if present
	switch e.Kind {
	case Notice:
		var n NoticeData
		if err := json.Unmarshal(e.Data, &n); err == nil {
			if !n.ValidateStatus() {
				return nil, fmt.Errorf("%w: code=%s, status=%s", ErrInvalidNoticeStatus, n.Code, n.Status)
			}
			if n.Change != nil && !n.Change.Action.IsValid() {
				return nil, fmt.Errorf("%w: action=%s", ErrInvalidChangeAction, n.Change.Action)
			}
		}
	case FileEdit:
		var c ChangeData
		if err := json.Unmarshal(e.Data, &c); err == nil {
			if !c.Action.IsValid() {
				return nil, fmt.Errorf("%w: action=%s", ErrInvalidChangeAction, c.Action)
			}
		}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\r\n"), nil
}

// DecodeEvent decodes a single JSON line into an Event, disallowing unknown envelope fields.
func DecodeEvent(line []byte) (Event, error) {
	clean := bytes.TrimRight(line, "\r\n")
	if len(clean) == 0 {
		return Event{}, errors.New("empty event line")
	}

	var e Event
	d := json.NewDecoder(bytes.NewReader(clean))
	d.DisallowUnknownFields()
	if err := d.Decode(&e); err != nil {
		return Event{}, err
	}

	if e.Time.IsZero() || e.Time.Location() != time.UTC {
		return Event{}, ErrInvalidEventTime
	}
	if e.Kind == "" {
		return Event{}, ErrInvalidEventKind
	}
	if len(e.Data) == 0 {
		return Event{}, ErrMissingEventData
	}
	return e, nil
}

// CanonicalizeJSON produces a deterministic, whitespace-free JSON representation
// with lexicographically sorted object keys and unescaped HTML characters.
func CanonicalizeJSON(raw []byte) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte("{}"), nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var val any
	if err := d.Decode(&val); err != nil {
		return nil, err
	}
	return marshalCanonical(val)
}

func marshalCanonical(v any) ([]byte, error) {
	switch val := v.(type) {
	case nil:
		return []byte("null"), nil
	case bool:
		if val {
			return []byte("true"), nil
		}
		return []byte("false"), nil
	case json.Number:
		return []byte(val.String()), nil
	case string:
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(val); err != nil {
			return nil, err
		}
		return bytes.TrimRight(buf.Bytes(), "\r\n"), nil
	case []any:
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, item := range val {
			if i > 0 {
				buf.WriteByte(',')
			}
			b, err := marshalCanonical(item)
			if err != nil {
				return nil, err
			}
			buf.Write(b)
		}
		buf.WriteByte(']')
		return buf.Bytes(), nil
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var buf bytes.Buffer
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, _ := marshalCanonical(k)
			buf.Write(kb)
			buf.WriteByte(':')
			vb, err := marshalCanonical(val[k])
			if err != nil {
				return nil, err
			}
			buf.Write(vb)
		}
		buf.WriteByte('}')
		return buf.Bytes(), nil
	default:
		return nil, fmt.Errorf("unsupported type for canonicalization: %T", v)
	}
}

// ComputeRequestHash binds consent to canonical tool name, canonical args object, and resolved working directory.
// Returns a 64-character lowercase SHA-256 hexadecimal string.
func ComputeRequestHash(name string, args json.RawMessage, cwd string) (string, error) {
	cleanName := strings.TrimSpace(name)
	if cleanName == "" {
		return "", errors.New("tool name cannot be empty")
	}

	canonicalArgs, err := CanonicalizeJSON(args)
	if err != nil {
		return "", fmt.Errorf("canonicalizing args: %w", err)
	}

	// Normalize CWD to clean, forward-slash representation
	normalizedCwd := filepath.ToSlash(filepath.Clean(cwd))

	// Envelope: {"args":<canonical_args>,"cwd":"<normalized_cwd>","name":"<name>"}
	var buf bytes.Buffer
	buf.WriteString(`{"args":`)
	buf.Write(canonicalArgs)
	buf.WriteString(`,"cwd":`)
	cwdb, _ := marshalCanonical(normalizedCwd)
	buf.Write(cwdb)
	buf.WriteString(`,"name":`)
	nameb, _ := marshalCanonical(cleanName)
	buf.Write(nameb)
	buf.WriteString(`}`)

	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:]), nil
}
