package tools

import (
	"bytes"
	"encoding/json"
	"errors"
)

// decodeStrict decodes raw into dst while rejecting duplicate keys and unknown
// fields. It uses a *string/*bool target so absent and null both yield nil.
//
// It is the shared argument boundary: write_file, edit_file, and bash all
// decode through it, so a call whose JSON says more than the tool declares is
// rejected before any side effect rather than being silently reinterpreted
// (json.Unmarshal keeps the last value of a duplicate key and drops unknown
// keys).
func decodeStrict(raw json.RawMessage, dst interface{}) error {
	// Duplicate-key detection: encoding/json silently takes the last value.
	// We decode into a map once and compare key counts; a mismatch means a
	// key appeared more than once.
	if err := rejectDuplicateKeys(raw); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

// rejectDuplicateKeys reports an error if raw (a JSON object) contains any key
// more than once. Non-object top-level values are ignored here (the typed
// decode will reject them).
func rejectDuplicateKeys(raw json.RawMessage) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		// Not an object or invalid JSON: let the typed decoder report it.
		return nil
	}
	// Count raw top-level keys by tokenizing; maps collapse duplicates.
	if len(raw) > 0 && raw[0] == '{' {
		n := 0
		dec := json.NewDecoder(bytes.NewReader(raw))
		tok, err := dec.Token()
		if err == nil {
			if d, ok := tok.(json.Delim); ok && d == '{' {
				for dec.More() {
					_, _ = dec.Token() // key
					var skip json.RawMessage
					_ = dec.Decode(&skip) // value
					n++
				}
			}
		}
		if n > len(probe) {
			return errors.New("duplicate key in request")
		}
	}
	return nil
}
