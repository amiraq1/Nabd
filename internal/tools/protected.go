package tools

import "errors"

// ErrProtected is returned when a tool refuses to read or search a path that is
// protected by policy — for example a path component named .git or .ag inside
// the project root (P0-2 / F-02). Callers and tests must check it with
// errors.Is so that an ordinary "not found" or "permission denied" error is not
// mistaken for a security refusal, and so a missing-file error is never counted
// as a successful security block.
//
// This sentinel is the classification signal the P0-2 red-phase tests assert
// against. The enforcement that actually returns it from the read/grep paths is
// implemented in B1 (F-02); until then those tests are intentionally red.
var ErrProtected = errors.New("tools: protected path")
