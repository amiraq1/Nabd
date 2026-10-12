// Package approval carries a one-shot, narrowly-bound grant to write to a path
// that the session .gitignore excludes (ADR-5 Option A).
//
// The token exists because an excluded path must never be written silently: the
// content would not be journaled and could not be undone, so the write needs an
// explicit, auditable grant that names exactly what it authorises. A token is
// bound to three things at once — the canonical relative path, the tool-call id,
// and the process id — and is consumed exactly once. A token minted for one
// path, one call, or one process cannot be redeemed for another.
//
// The token is minted only by the agent loop, after the gate returned an
// allowing decision for the specific call. Tools never mint it; they only
// consume it. Keeping the type here (rather than in either package) avoids an
// import cycle: internal/tools imports internal/agent, so the shared contract
// cannot live in tools.
package approval

import (
	"context"
	"os"
	"sync/atomic"
)

// Token is a single-use grant bound to one (path, callID, pid) triple.
type Token struct {
	path   string
	callID string
	pid    int
	used   atomic.Bool
}

// Mint creates a token bound to path and callID and the current process id.
// Only the agent loop calls this, once per approved call.
func Mint(path, callID string) *Token {
	return &Token{path: path, callID: callID, pid: os.Getpid()}
}

type tokenKey struct{}
type callKey struct{}

// WithToken returns a context carrying t. Only the agent loop calls this.
func WithToken(ctx context.Context, t *Token) context.Context {
	return context.WithValue(ctx, tokenKey{}, t)
}

// WithCall returns a context carrying the id of the tool call about to run. The
// tool registry sets it from the provider call, so a token bound to one call
// cannot be redeemed for a different one.
func WithCall(ctx context.Context, callID string) context.Context {
	return context.WithValue(ctx, callKey{}, callID)
}

// Call returns the tool-call id carried by ctx, or "" when none was set.
func Call(ctx context.Context) string {
	id, _ := ctx.Value(callKey{}).(string)
	return id
}

// Consume redeems the token in ctx for path. It returns true only when a token
// is present and ALL of the following hold: it was bound to the same path, to
// the same call id that ctx carries, and to the current process, and it has not
// been used before. On success the token is marked used, so a second Consume
// with the same token returns false.
func Consume(ctx context.Context, path string) bool {
	t, _ := ctx.Value(tokenKey{}).(*Token)
	if t == nil {
		return false
	}
	if t.pid != os.Getpid() {
		return false
	}
	if t.path != path {
		return false
	}
	if t.callID != Call(ctx) {
		return false
	}
	return t.used.CompareAndSwap(false, true)
}
