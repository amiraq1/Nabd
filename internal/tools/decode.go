package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// maxJSONDepth bounds how deep tool arguments may nest. Pathological nesting is
// a denial-of-service shape, not a request: no tool argument reaches this depth.
const maxJSONDepth = 32

// errJSONTooDeep refuses nesting past maxJSONDepth. It is returned unchanged by
// rejectDuplicateKeys so the caller reads the real reason.
var errJSONTooDeep = fmt.Errorf("tool arguments nest deeper than %d levels", maxJSONDepth)

// duplicateKeyError names the repeated key, so a refusal says what to fix
// rather than only that something was wrong.
type duplicateKeyError struct{ key string }

func (e *duplicateKeyError) Error() string {
	return "duplicate key in request: " + strconv.Quote(e.key)
}

// decodeStrict decodes raw into dst while rejecting duplicate keys, unknown
// fields, nesting past maxJSONDepth, and any data after the first JSON value.
// It uses a *string/*bool target so absent and null both yield nil.
//
// It is the shared argument boundary: bash, read_file, write_file, edit_file,
// skill and mcp_tool all decode through it, so a call whose JSON says more than
// the tool declares is rejected before any side effect rather than being
// silently reinterpreted (json.Unmarshal keeps the last value of a duplicate
// key and drops unknown keys).
func decodeStrict(raw json.RawMessage, dst interface{}) error {
	if err := rejectDuplicateKeys(raw); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	// One value is the contract: a second value after it was ignored while the
	// first one still ran.
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("unexpected trailing data after JSON value")
	}
	return nil
}

// rejectDuplicateKeys reports a key repeated anywhere in raw, and refuses
// nesting past maxJSONDepth.
//
// Keys are compared case-insensitively because encoding/json matches struct
// fields that way: {"cmd":…,"Cmd":…} would otherwise land twice on one field
// with the last value winning, which the top-level exact-case key count this
// replaces could not see. The walk covers every object, not only the root, so
// a duplicate inside an opaque field (mcp_tool's args) is refused too.
//
// A syntax error is suppressed here rather than reported: the typed decode in
// decodeStrict owns that message, so malformed input is never described as a
// duplicate or as too deep.
func rejectDuplicateKeys(raw json.RawMessage) error {
	err := walkJSONKeys(json.NewDecoder(bytes.NewReader(raw)), 1)
	if err == nil || errors.Is(err, errJSONTooDeep) {
		return err
	}
	var dup *duplicateKeyError
	if errors.As(err, &dup) {
		return dup
	}
	return nil
}

// walkJSONKeys walks one JSON value. depth is the container about to be
// entered, so the root value is depth 1 and a document nested exactly
// maxJSONDepth containers deep still passes.
func walkJSONKeys(dec *json.Decoder, depth int) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil // a scalar carries no keys and no containers
	}
	if depth > maxJSONDepth {
		return errJSONTooDeep
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for dec.More() {
			ktok, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := ktok.(string)
			if !ok {
				return errors.New("invalid object key")
			}
			folded := strings.ToLower(key)
			if _, dup := seen[folded]; dup {
				return &duplicateKeyError{key: key}
			}
			seen[folded] = struct{}{}
			if err := walkJSONKeys(dec, depth+1); err != nil {
				return err
			}
		}
		_, err = dec.Token() // the closing '}'
		return err
	case '[':
		for dec.More() {
			if err := walkJSONKeys(dec, depth+1); err != nil {
				return err
			}
		}
		_, err = dec.Token() // the closing ']'
		return err
	}
	return nil
}
