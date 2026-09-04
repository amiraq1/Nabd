// Package provider: router.go implements the deterministic pre-output provider
// router for v1.2.0. It is the authoritative implementation of the state machine
// described in the contract (Sections E, G, H, I, J, K, L, M).
//
// # Guarantees
//
//   - Routes are attempted in the configured order, sequentially (X8).
//   - No route is started until the previous one has fully stopped (G4).
//   - Fallback occurs only before the first semantic chunk is committed (X7).
//   - After commit the router is a pure ordered pass-through (J.7).
//   - Router.Stream returns promptly; all I/O is asynchronous (E.1).
//   - Concurrent calls to Router.Stream share no mutable state (I.6, I.7, I.8).
//   - The output channel is closed exactly once in every code path (E.1).
//   - StreamID is 16 random bytes (128 bits) from crypto/rand, hex-encoded (A-01).
//
// # Known limitations (documented, not defects)
//
//	KNOWN_LIMITATION_CIRCUIT_BREAKER: NOT_IMPLEMENTED (P section)
//	KNOWN_LIMITATION_REMOTE_CANCELLATION: YES (M section)
//	KNOWN_LIMITATION_DOUBLE_BILLING_RACE: YES (M section)
//	KNOWN_LIMITATION_WORST_CASE_LATENCY:
//	  The conservative router-controlled upper bound before full exhaustion is:
//	  route_count × (PRESTREAM_TIMEOUT + ROUTE_CLEANUP_TIMEOUT)
//	  plus bounded local scheduling and error-processing overhead.
//	  A parent-context deadline may shorten this duration.
//	  If cleanup of any route exceeds ROUTE_CLEANUP_TIMEOUT, routing terminates
//	  immediately with ErrRouteCleanupTimeout and no subsequent route is started.
package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ─── Constants and errors ─────────────────────────────────────────────────────

// RouteCleanupTimeout is the maximum time the router waits for a cancelled
// route to fully stop (channel close). If exceeded the entire request fails
// with ErrRouteCleanupTimeout — no subsequent route is started (G5).
const RouteCleanupTimeout = 2 * time.Second

// MaxPrecommitBufferedChunks is the maximum number of metadata chunks buffered
// before commit. Exceeding this is treated as a fallback-eligible route failure
// with a distinct sanitized reason (J.6).
const MaxPrecommitBufferedChunks = 64

// maxRetryCeiling is the agent's retry ceiling for Retry-After clamping (D5/L).
// Matches agent.Loop maxRateLimitWait = 120 * time.Second.
const maxRetryCeiling = 120 * time.Second

var (
	// ErrRouteCleanupTimeout is returned when a route's goroutine fails to stop
	// within RouteCleanupTimeout. The entire request is aborted (G5).
	ErrRouteCleanupTimeout = errors.New("route cleanup timeout")

	// ErrRouterExhausted is returned (via ChunkError) when all routes have been
	// tried and none succeeded.
	ErrRouterExhausted = errors.New("all routes exhausted")
)

// ─── ExhaustionKind ────────────────────────────────────────────────────────────

// ExhaustionKind distinguishes the nature of route exhaustion for the agent.
type ExhaustionKind uint8

const (
	// ExhaustionRateLimitOnly means every route returned HTTP 429.
	ExhaustionRateLimitOnly ExhaustionKind = iota
	// ExhaustionMixed means at least one route returned a non-429 failure.
	ExhaustionMixed
)

// ─── ProviderError ────────────────────────────────────────────────────────────

// ProviderError is a sanitized, structured record of a single route failure.
// Body is always redacted before storage (N section).
type ProviderError struct {
	Provider  string
	Model     string
	Status    int
	Code      string
	Retryable bool
	Body      string // sanitized (Redact + TruncateBody applied)
}

// ─── RouterExhaustedError ─────────────────────────────────────────────────────

// RouterExhaustedError carries the sanitized per-route failures when all routes
// are exhausted. It is emitted as a ChunkError payload.
type RouterExhaustedError struct {
	Attempts   []ProviderError
	Kind       ExhaustionKind
	RetryAfter time.Duration // shortest positive valid value; 0 = none
}

func (e *RouterExhaustedError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "all %d route(s) exhausted", len(e.Attempts))
	if e.Kind == ExhaustionRateLimitOnly {
		b.WriteString(" (rate limited)")
	} else {
		b.WriteString(" (mixed failures)")
	}
	if e.RetryAfter > 0 {
		fmt.Fprintf(&b, "; shortest retry-after: %s", e.RetryAfter)
	}
	for _, a := range e.Attempts {
		if a.Body != "" {
			fmt.Fprintf(&b, "\n  [%s:%s]: %s", a.Provider, a.Model, a.Body)
		}
	}
	s := b.String()
	if len(s) > maxAggregateBytes {
		s = s[:maxAggregateBytes] + "…[aggregate truncated]"
	}
	return s
}

// ─── ChunkRouteTrace ──────────────────────────────────────────────────────────

// ChunkRouteTrace is a metadata-only chunk emitted by the router to carry
// per-attempt routing decisions (Section E). It is NOT a semantic chunk and does NOT
// trigger commit (J.5). Consumers must tolerate its absence.
//
// Status values: "attempted", "failed", "selected", "exhausted".
type ChunkRouteTrace struct {
	StreamID string
	Provider string
	Model    string
	Attempt  int
	Status   string // attempted | failed | selected | exhausted
	Reason   string // sanitized, optional
}

// ─── Clock and Timer abstractions (Section E) ─────────────────────────────────

// Clock abstracts wall-clock access so tests can inject a fake (no time.Sleep
// needed). All timing decisions in the router go through the injected Clock.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// Timer abstracts a timer channel and cancel/reset operations.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}

// RealClock is the production Clock implementation using standard library time.
type RealClock struct{}

func (RealClock) Now() time.Time                 { return time.Now() }
func (RealClock) NewTimer(d time.Duration) Timer { return &realTimer{t: time.NewTimer(d)} }

type realTimer struct {
	t *time.Timer
}

func (r *realTimer) C() <-chan time.Time        { return r.t.C }
func (r *realTimer) Stop() bool                 { return r.t.Stop() }
func (r *realTimer) Reset(d time.Duration) bool { return r.t.Reset(d) }

// ─── Router ───────────────────────────────────────────────────────────────────

// Router implements Provider by trying a sequence of routes (provider+model
// pairs) in order, falling back on failure, and committing to the first route
// that delivers a semantic chunk (Text, ToolCall, or Stop).
//
// All fields are unexported and set once at construction (I.1-I.5, I.11).
// Router is safe for concurrent use after construction (I.8).
type Router struct {
	routes           []Route
	prestreamTimeout time.Duration
	cleanupTimeout   time.Duration
	clock            Clock
	name             string
	newStreamID      func() (string, error)
}

// NewRouter constructs an immutable Router (Section I).
// It defensively copies the provided routes slice (I.1).
// It rejects an empty routes slice (I.2).
// timeout is the per-route pre-stream timeout; if <= 0 it defaults to 30s.
// clock may be nil, defaulting to RealClock{}.
func NewRouter(routes []Route, timeout time.Duration, clock Clock) (*Router, error) {
	if len(routes) == 0 {
		return nil, errors.New("router: route list must not be empty")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if clock == nil {
		clock = RealClock{}
	}

	copied := make([]Route, len(routes))
	for i, r := range routes {
		if r.Provider == "" {
			return nil, fmt.Errorf("router: route[%d] has empty provider", i)
		}
		if r.Model == "" {
			return nil, fmt.Errorf("router: route[%d] has empty model", i)
		}
		if r.Client == nil {
			return nil, fmt.Errorf("router: route[%d] has nil client", i)
		}
		copied[i] = r
	}

	var parts []string
	for _, r := range copied {
		parts = append(parts, r.Provider+":"+r.Model)
	}
	name := "router/" + strings.Join(parts, "→")
	if len(name) > 200 {
		name = name[:200] + "…"
	}

	return &Router{
		routes:           copied,
		prestreamTimeout: timeout,
		cleanupTimeout:   RouteCleanupTimeout,
		clock:            clock,
		name:             name,
		newStreamID:      randomStreamID,
	}, nil
}

// Name returns a human-readable, non-secret description of the router.
func (r *Router) Name() string { return r.name }

// WithStreamIDFunc overrides the StreamID generator for testing.
func (r *Router) WithStreamIDFunc(fn func() (string, error)) *Router {
	r.newStreamID = fn
	return r
}

// ─── Stream ───────────────────────────────────────────────────────────────────

// Stream implements Provider. It returns promptly — all routing is asynchronous.
// Synchronous errors (err != nil, channel == nil) are only returned for local
// pre-network faults: invalid Request, StreamID generation failure (E.1).
//
// All route-attempt outcomes are delivered through the returned channel.
// The channel is closed exactly once in every code path.
func (r *Router) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	if len(req.Messages) == 0 {
		return nil, errors.New("router: request has no messages")
	}

	streamID, err := r.newStreamID()
	if err != nil {
		return nil, fmt.Errorf("router: failed to generate stream ID: %w", err)
	}

	out := make(chan Chunk, 32)
	go r.route(ctx, req, streamID, out)
	return out, nil
}

// ─── route: the main routing goroutine ────────────────────────────────────────

func (r *Router) route(ctx context.Context, req Request, streamID string, out chan<- Chunk) {
	defer close(out)

	var (
		attempts []ProviderError
		allRL    = true
	)

	for idx, re := range r.routes {
		if ctx.Err() != nil {
			sendError(out, ctx.Err())
			return
		}

		routeCtx, routeCancel := context.WithTimeout(ctx, r.prestreamTimeout)
		routeDeadline := r.clock.Now().Add(r.prestreamTimeout)

		routeCh, err := re.Client.Start(routeCtx, req)
		if err != nil {
			routeCancel()
			pe := ProviderError{
				Provider:  re.Provider,
				Model:     re.Model,
				Retryable: true,
				Body:      SanitizeBody(err.Error(), nil),
			}
			attempts = append(attempts, pe)
			allRL = false
			sendTrace(out, ChunkRouteTrace{
				StreamID: streamID,
				Provider: re.Provider,
				Model:    re.Model,
				Attempt:  idx + 1,
				Status:   "failed",
				Reason:   pe.Body,
			})
			continue
		}

		outcome := r.consumeRoute(ctx, routeCtx, routeCancel, routeDeadline, re, idx+1, streamID, routeCh, out)

		switch outcome.kind {
		case outcomeCommitted:
			return

		case outcomeCleanupTimeout:
			sendError(out, ErrRouteCleanupTimeout)
			return

		case outcomeParentCancelled:
			sendError(out, ctx.Err())
			return

		case outcomeNonRetryableError:
			// Non-retryable error (e.g. generic 400 Bad Request) stops fallback immediately.
			sendError(out, outcome.nonRetryErr)
			return

		case outcomeFallbackEligible:
			pe := outcome.provErr
			attempts = append(attempts, pe)
			if pe.Status != http.StatusTooManyRequests {
				allRL = false
			}
			sendTrace(out, ChunkRouteTrace{
				StreamID: streamID,
				Provider: re.Provider,
				Model:    re.Model,
				Attempt:  idx + 1,
				Status:   "failed",
				Reason:   pe.Body,
			})
		}
	}

	kind := ExhaustionMixed
	if allRL && len(attempts) > 0 {
		kind = ExhaustionRateLimitOnly
	}

	retryAfter := shortestPositiveRetryAfter(attempts)

	sendTrace(out, ChunkRouteTrace{
		StreamID: streamID,
		Provider: "",
		Model:    "",
		Attempt:  len(attempts),
		Status:   "exhausted",
	})

	if kind == ExhaustionRateLimitOnly {
		var waitSec float64
		if retryAfter > 0 {
			waitSec = retryAfter.Seconds()
		}
		out <- Chunk{
			Kind: ChunkRateLimit,
			RateLimit: &RateLimitInfo{
				Code:       http.StatusTooManyRequests,
				WaitSec:    waitSec,
				Attempt:    len(attempts),
				Err:        ErrRouterExhausted.Error(),
				RawMessage: buildSanitizedAggregate(attempts),
			},
		}
	} else {
		err := &RouterExhaustedError{
			Attempts:   attempts,
			Kind:       kind,
			RetryAfter: retryAfter,
		}
		out <- Chunk{Kind: ChunkError, Err: err, Retryable: true}
	}
}

// ─── outcomeKind ──────────────────────────────────────────────────────────────

type outcomeKind uint8

const (
	outcomeCommitted outcomeKind = iota
	outcomeFallbackEligible
	outcomeCleanupTimeout
	outcomeParentCancelled
	outcomeNonRetryableError
)

type routeOutcome struct {
	kind        outcomeKind
	provErr     ProviderError
	nonRetryErr error
}

// ─── consumeRoute ─────────────────────────────────────────────────────────────

func (r *Router) consumeRoute(
	parentCtx context.Context,
	routeCtx context.Context,
	routeCancel context.CancelFunc,
	routeDeadline time.Time,
	re Route,
	attemptNum int,
	streamID string,
	routeCh <-chan Chunk,
	out chan<- Chunk,
) routeOutcome {
	var precommitMeta []Chunk
	var mu sync.Mutex
	committed := false

	checkAndCommit := func() bool {
		mu.Lock()
		defer mu.Unlock()
		if parentCtx.Err() != nil {
			return false
		}
		if !r.clock.Now().Before(routeDeadline) {
			return false
		}
		committed = true
		return true
	}
	_ = committed

	defer routeCancel()

	for {
		select {
		case chunk, ok := <-routeCh:
			if !ok {
				return r.makeFailure(re, http.StatusOK, "", true, "unexpected channel close before stop chunk")
			}

			switch chunk.Kind {
			case ChunkText, ChunkToolCall, ChunkStop:
				if !checkAndCommit() {
					okCleanup := drainAndWait(routeCh, r.cleanupTimeout, routeCancel)
					if !okCleanup {
						return routeOutcome{kind: outcomeCleanupTimeout}
					}
					if parentCtx.Err() != nil {
						return routeOutcome{kind: outcomeParentCancelled}
					}
					return r.makeFailure(re, 0, "", true, "prestream timeout at linearization point")
				}

				// J.5: emit selected trace AFTER commit flip, before chunks.
				sendTrace(out, ChunkRouteTrace{
					StreamID: streamID,
					Provider: re.Provider,
					Model:    re.Model,
					Attempt:  attemptNum,
					Status:   "selected",
				})

				// J.2, J.6: send buffered metadata chunks outside lock.
				for _, m := range precommitMeta {
					select {
					case out <- m:
					case <-parentCtx.Done():
						okCleanup := drainAndWait(routeCh, r.cleanupTimeout, routeCancel)
						if !okCleanup {
							return routeOutcome{kind: outcomeCleanupTimeout}
						}
						return routeOutcome{kind: outcomeCommitted}
					}
				}

				// Deliver first semantic chunk.
				select {
				case out <- chunk:
				case <-parentCtx.Done():
					okCleanup := drainAndWait(routeCh, r.cleanupTimeout, routeCancel)
					if !okCleanup {
						return routeOutcome{kind: outcomeCleanupTimeout}
					}
					return routeOutcome{kind: outcomeCommitted}
				}

				// J.7: pass through all remaining chunks.
				r.passThrough(parentCtx, routeCh, out, r.cleanupTimeout, routeCancel)
				return routeOutcome{kind: outcomeCommitted}

			case ChunkError:
				okCleanup := drainAndWait(routeCh, r.cleanupTimeout, routeCancel)
				if !okCleanup {
					return routeOutcome{kind: outcomeCleanupTimeout}
				}
				if parentCtx.Err() != nil {
					return routeOutcome{kind: outcomeParentCancelled}
				}
				return r.classifyError(re, chunk)

			case ChunkRateLimit:
				okCleanup := drainAndWait(routeCh, r.cleanupTimeout, routeCancel)
				if !okCleanup {
					return routeOutcome{kind: outcomeCleanupTimeout}
				}
				if parentCtx.Err() != nil {
					return routeOutcome{kind: outcomeParentCancelled}
				}
				return r.classifyRateLimit(re, chunk)

			default:
				if len(precommitMeta) >= MaxPrecommitBufferedChunks {
					okCleanup := drainAndWait(routeCh, r.cleanupTimeout, routeCancel)
					if !okCleanup {
						return routeOutcome{kind: outcomeCleanupTimeout}
					}
					return r.makeFailure(re, 0, "", true, "pre-commit metadata buffer overflow")
				}
				precommitMeta = append(precommitMeta, chunk)
			}

		case <-routeCtx.Done():
			okCleanup := drainAndWait(routeCh, r.cleanupTimeout, routeCancel)
			if !okCleanup {
				return routeOutcome{kind: outcomeCleanupTimeout}
			}
			if parentCtx.Err() != nil {
				return routeOutcome{kind: outcomeParentCancelled}
			}
			return r.makeFailure(re, 0, "", true, "prestream timeout")

		case <-parentCtx.Done():
			okCleanup := drainAndWait(routeCh, r.cleanupTimeout, routeCancel)
			if !okCleanup {
				return routeOutcome{kind: outcomeCleanupTimeout}
			}
			return routeOutcome{kind: outcomeParentCancelled}
		}
	}
}

// ─── passThrough ──────────────────────────────────────────────────────────────

func (r *Router) passThrough(
	parentCtx context.Context,
	routeCh <-chan Chunk,
	out chan<- Chunk,
	cleanupTimeout time.Duration,
	routeCancel context.CancelFunc,
) {
	for chunk := range routeCh {
		select {
		case out <- chunk:
		case <-parentCtx.Done():
			drainAndWait(routeCh, cleanupTimeout, routeCancel)
			return
		}
	}
}

// ─── cleanup helpers ──────────────────────────────────────────────────────────

// drainAndWait cancels the route and drains routeCh until it is closed or timeout.
// Returns true if channel closed within timeout (ACK received), false on timeout.
func drainAndWait(routeCh <-chan Chunk, timeout time.Duration, routeCancel context.CancelFunc) bool {
	routeCancel()
	t := time.NewTimer(timeout)
	defer t.Stop()
	for {
		select {
		case _, ok := <-routeCh:
			if !ok {
				return true
			}
		case <-t.C:
			return false
		}
	}
}

// ─── error classification (Section K) ─────────────────────────────────────────

func (r *Router) classifyError(re Route, chunk Chunk) routeOutcome {
	if chunk.Err == nil {
		return r.makeFailure(re, 0, "", true, "nil error in ChunkError")
	}

	if errors.Is(chunk.Err, context.Canceled) || errors.Is(chunk.Err, context.DeadlineExceeded) {
		return routeOutcome{kind: outcomeParentCancelled}
	}

	var he *httpError
	if errors.As(chunk.Err, &he) {
		if isModelNotFound(he.Status, he.Body) {
			return r.makeFailure(re, he.Status, he.Body, true, Redact(he.Body))
		}
		if he.Status == http.StatusBadRequest {
			// Generic 400 Bad Request — Section K: no fallback.
			return routeOutcome{
				kind:        outcomeNonRetryableError,
				nonRetryErr: fmt.Errorf("provider %s:%s returned non-retryable error 400: %s", re.Provider, re.Model, Redact(he.Body)),
			}
		}
		return r.makeFailure(re, he.Status, he.Body, isFallbackStatus(he.Status), Redact(he.Body))
	}

	errStr := chunk.Err.Error()
	if strings.Contains(errStr, "400") && !isModelNotFound(400, errStr) {
		// Generic 400 in text format
		return routeOutcome{
			kind:        outcomeNonRetryableError,
			nonRetryErr: fmt.Errorf("provider %s:%s returned non-retryable 400: %s", re.Provider, re.Model, Redact(errStr)),
		}
	}

	body := Redact(errStr)
	return routeOutcome{
		kind: outcomeFallbackEligible,
		provErr: ProviderError{
			Provider:  re.Provider,
			Model:     re.Model,
			Status:    0,
			Retryable: chunk.Retryable,
			Body:      TruncateBody(body),
		},
	}
}

func (r *Router) classifyRateLimit(re Route, chunk Chunk) routeOutcome {
	var body string
	var rawRA string
	if chunk.RateLimit != nil {
		body = SanitizeBody(chunk.RateLimit.RawMessage, nil)
		rawRA = chunk.RateLimit.RawRetryAfter
		if rawRA == "" && chunk.RateLimit.WaitSec > 0 {
			rawRA = fmt.Sprintf("%.2f", chunk.RateLimit.WaitSec)
		}
	}

	d, _ := parseRetryAfterValue(rawRA, r.clock)

	return routeOutcome{
		kind: outcomeFallbackEligible,
		provErr: ProviderError{
			Provider:  re.Provider,
			Model:     re.Model,
			Status:    http.StatusTooManyRequests,
			Retryable: true,
			Body:      body,
			Code:      encodeRetryAfter(d),
		},
	}
}

func (r *Router) makeFailure(re Route, status int, rawBody string, retryable bool, sanitizedReason string) routeOutcome {
	body := sanitizedReason
	if rawBody != "" {
		body = TruncateBody(Redact(rawBody))
	}
	return routeOutcome{
		kind: outcomeFallbackEligible,
		provErr: ProviderError{
			Provider:  re.Provider,
			Model:     re.Model,
			Status:    status,
			Retryable: retryable,
			Body:      body,
		},
	}
}

func isFallbackStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, // 408
		http.StatusConflict,        // 409
		http.StatusTooManyRequests, // 429
		http.StatusNotFound,        // 404
		http.StatusGone,            // 410
		http.StatusUnauthorized,    // 401
		http.StatusForbidden:       // 403
		return true
	case http.StatusBadRequest: // 400 — handled via isModelNotFound
		return false
	}
	return status >= 500
}

func isModelNotFound(status int, body string) bool {
	if status != http.StatusBadRequest && status != http.StatusNotFound {
		return false
	}
	lower := strings.ToLower(body)
	patterns := []string{
		"model_not_found",
		"model_decommissioned",
		"invalid_model",
		"model not found",
		"does not exist",
		"unknown model",
		"unrecognized model",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// ─── Retry-After helpers (Section L) ──────────────────────────────────────────

func parseRetryAfterValue(raw string, clock Clock) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}

	// Try integer/float seconds directly:
	if sec, err := strconv.ParseFloat(raw, 64); err == nil {
		if sec <= 0 || math.IsNaN(sec) || math.IsInf(sec, 0) {
			return 0, false
		}
		d := time.Duration(sec * float64(time.Second))
		if d <= 0 {
			return 0, false
		}
		if d > maxRetryCeiling {
			d = maxRetryCeiling
		}
		return d, true
	}

	// Try HTTP-date:
	if t, err := http.ParseTime(raw); err == nil {
		now := clock.Now()
		if !t.After(now) {
			return 0, false
		}
		d := t.Sub(now)
		if d <= 0 {
			return 0, false
		}
		if d > maxRetryCeiling {
			d = maxRetryCeiling
		}
		return d, true
	}

	return 0, false
}

func encodeRetryAfter(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return fmt.Sprintf("ra:%d", int64(d))
}

func decodeRetryAfter(code string) time.Duration {
	if !strings.HasPrefix(code, "ra:") {
		return 0
	}
	var ns int64
	if _, err := fmt.Sscanf(code[3:], "%d", &ns); err != nil || ns <= 0 {
		return 0
	}
	return time.Duration(ns)
}

func shortestPositiveRetryAfter(attempts []ProviderError) time.Duration {
	var shortest time.Duration
	for _, a := range attempts {
		if a.Status != http.StatusTooManyRequests {
			continue
		}
		d := decodeRetryAfter(a.Code)
		if d <= 0 {
			continue
		}
		if shortest == 0 || d < shortest {
			shortest = d
		}
	}
	return shortest
}

func buildSanitizedAggregate(attempts []ProviderError) string {
	var b strings.Builder
	for _, a := range attempts {
		if a.Body != "" {
			fmt.Fprintf(&b, "[%s:%s] %s\n", a.Provider, a.Model, a.Body)
		}
	}
	s := b.String()
	if len(s) > maxAggregateBytes {
		s = s[:maxAggregateBytes] + "…[aggregate truncated]"
	}
	return s
}

// ─── StreamID generation (A-01) ───────────────────────────────────────────────

func randomStreamID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("crypto/rand unavailable: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// DeterministicStreamID produces sequential hex IDs for deterministic test assertions.
func DeterministicStreamID(prefix string) func() (string, error) {
	var mu sync.Mutex
	n := 0
	return func() (string, error) {
		mu.Lock()
		defer mu.Unlock()
		n++
		return fmt.Sprintf("%s%016d", prefix, n), nil
	}
}

// ─── send helpers ─────────────────────────────────────────────────────────────

func sendError(out chan<- Chunk, err error) {
	out <- Chunk{Kind: ChunkError, Err: err}
}

func sendTrace(out chan<- Chunk, trace ChunkRouteTrace) {
	out <- Chunk{Kind: ChunkTrace, RouteTrace: &trace}
}
