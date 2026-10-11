package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// FingerprintTool returns the hex SHA-256 over JCS-canonical JSON
// (RFC 8785-inspired, deterministic) of {"server": serverID, "tool": obj}.
// Every field the server advertised — name, description, inputSchema, hints,
// and any other field — is covered, so any change invalidates the approval
// (MCP-SEC-06/07).
func FingerprintTool(serverID string, toolJSON json.RawMessage) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(toolJSON))
	dec.UseNumber()
	var obj any
	if err := dec.Decode(&obj); err != nil {
		return "", fmt.Errorf("mcp: bad tool JSON: %w", err)
	}
	if dec.More() {
		return "", errors.New("mcp: trailing content in tool JSON")
	}
	if _, ok := obj.(map[string]any); !ok {
		return "", errors.New("mcp: tool JSON is not an object")
	}
	composite := map[string]any{"server": serverID, "tool": obj}
	var buf bytes.Buffer
	writeCanonical(&buf, composite)
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:]), nil
}

// CanonicalJSON returns the deterministic canonical form of raw JSON.
// It is exported for tests and for approval-content debugging.
func CanonicalJSON(raw json.RawMessage) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("mcp: trailing content")
	}
	var buf bytes.Buffer
	writeCanonical(&buf, v)
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		b, _ := json.Marshal(t)
		buf.Write(b)
	case json.Number:
		buf.WriteString(canonicalNumber(t.String()))
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeCanonical(buf, e)
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			buf.Write(kb)
			buf.WriteByte(':')
			writeCanonical(buf, t[k])
		}
		buf.WriteByte('}')
	default:
		// Should not happen after UseNumber decoding; be deterministic anyway.
		b, _ := json.Marshal(t)
		buf.Write(b)
	}
}

// canonicalNumber renders a JSON number deterministically: integers without
// leading zeros ("-0" becomes "0"), floats in shortest round-trip form.
func canonicalNumber(s string) string {
	if bi, ok := new(big.Int).SetString(s, 10); ok {
		// Pure integer (arbitrary precision preserved).
		return bi.String()
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s // not a number; preserve verbatim
	}
	out := strconv.FormatFloat(f, 'g', -1, 64)
	// JCS/ES6 style exponent: "1e+21" -> "1e21".
	out = strings.Replace(out, "e+", "e", 1)
	return out
}
