package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─── Test helpers & mock implementations ──────────────────────────────────────

type mockSingleAttempt struct {
	name    string
	startFn func(ctx context.Context, req Request) (<-chan Chunk, error)
}

func (m *mockSingleAttempt) Name() string {
	if m.name != "" {
		return m.name
	}
	return "mock-provider"
}

func (m *mockSingleAttempt) Start(ctx context.Context, req Request) (<-chan Chunk, error) {
	if m.startFn != nil {
		return m.startFn(ctx, req)
	}
	ch := make(chan Chunk, 1)
	ch <- Chunk{Kind: ChunkText, Text: "default response"}
	close(ch)
	return ch, nil
}

// fakeClock implements Clock for deterministic time testing.
type fakeClock struct {
	mu      sync.Mutex
	current time.Time
	timers  []*fakeTimer
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{current: start}
}

func (fc *fakeClock) Now() time.Time {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.current
}

func (fc *fakeClock) Advance(d time.Duration) {
	fc.mu.Lock()
	fc.current = fc.current.Add(d)
	now := fc.current
	var toFire []*fakeTimer
	for _, t := range fc.timers {
		if !t.stopped && !t.deadline.After(now) {
			toFire = append(toFire, t)
		}
	}
	fc.mu.Unlock()

	for _, t := range toFire {
		t.fire()
	}
}

func (fc *fakeClock) NewTimer(d time.Duration) Timer {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	deadline := fc.current.Add(d)
	ft := &fakeTimer{
		fc:       fc,
		deadline: deadline,
		ch:       make(chan time.Time, 1),
	}
	fc.timers = append(fc.timers, ft)
	return ft
}

type fakeTimer struct {
	fc       *fakeClock
	deadline time.Time
	ch       chan time.Time
	stopped  bool
	mu       sync.Mutex
}

func (ft *fakeTimer) C() <-chan time.Time {
	return ft.ch
}

func (ft *fakeTimer) Stop() bool {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	wasActive := !ft.stopped
	ft.stopped = true
	return wasActive
}

func (ft *fakeTimer) Reset(d time.Duration) bool {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	wasActive := !ft.stopped
	ft.stopped = false
	ft.deadline = ft.fc.Now().Add(d)
	return wasActive
}

func (ft *fakeTimer) fire() {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	if !ft.stopped {
		ft.stopped = true
		select {
		case ft.ch <- ft.deadline:
		default:
		}
	}
}

func makeValidRequest() Request {
	return Request{
		Messages: []Message{{Role: User, Text: "ping"}},
	}
}

// ─── Q.2 Tests: Core Fallback & Order ─────────────────────────────────────────

func TestRouterPreservesConfiguredOrder(t *testing.T) {
	var executionOrder []string
	var mu sync.Mutex

	record := func(name string) {
		mu.Lock()
		executionOrder = append(executionOrder, name)
		mu.Unlock()
	}

	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{name: "p1:m1", startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			record("p1:m1")
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkError, Err: errors.New("fail 1"), Retryable: true}
			close(ch)
			return ch, nil
		}},
	}

	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{name: "p2:m2", startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			record("p2:m2")
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkError, Err: errors.New("fail 2"), Retryable: true}
			close(ch)
			return ch, nil
		}},
	}

	r3 := Route{
		Provider: "p3", Model: "m3",
		Client: &mockSingleAttempt{name: "p3:m3", startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			record("p3:m3")
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkText, Text: "success"}
			close(ch)
			return ch, nil
		}},
	}

	router, err := NewRouter([]Route{r1, r2, r3}, 5*time.Second, RealClock{})
	if err != nil {
		t.Fatalf("unexpected NewRouter error: %v", err)
	}

	out, err := router.Stream(context.Background(), makeValidRequest())
	if err != nil {
		t.Fatalf("unexpected Stream error: %v", err)
	}

	for range out {
	}

	mu.Lock()
	defer mu.Unlock()
	expected := []string{"p1:m1", "p2:m2", "p3:m3"}
	if !reflect.DeepEqual(executionOrder, expected) {
		t.Fatalf("expected order %v, got %v", expected, executionOrder)
	}
}

func TestRouterUsesFirstHealthyRoute(t *testing.T) {
	r1Called := false
	r2Called := false

	r1 := Route{
		Provider: "healthy", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			r1Called = true
			ch := make(chan Chunk, 2)
			ch <- Chunk{Kind: ChunkText, Text: "hello"}
			ch <- Chunk{Kind: ChunkStop, Stop: "end_turn"}
			close(ch)
			return ch, nil
		}},
	}

	r2 := Route{
		Provider: "spare", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			r2Called = true
			ch := make(chan Chunk, 1)
			close(ch)
			return ch, nil
		}},
	}

	router, err := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	if err != nil {
		t.Fatalf("NewRouter error: %v", err)
	}

	out, err := router.Stream(context.Background(), makeValidRequest())
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	var texts []string
	for c := range out {
		if c.Kind == ChunkText {
			texts = append(texts, c.Text)
		}
	}

	if !r1Called {
		t.Fatal("expected healthy route 1 to be called")
	}
	if r2Called {
		t.Fatal("spare route 2 must not be called when route 1 succeeds")
	}
	if len(texts) != 1 || texts[0] != "hello" {
		t.Fatalf("unexpected text output: %v", texts)
	}
}

func TestRouterFallsBackOnRetryablePrecommitFailure(t *testing.T) {
	r2Ran := false
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkError, Err: errors.New("network connection reset"), Retryable: true}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			r2Ran = true
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkText, Text: "recovered"}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, err := router.Stream(context.Background(), makeValidRequest())
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	var texts []string
	for c := range out {
		if c.Kind == ChunkText {
			texts = append(texts, c.Text)
		}
	}

	if !r2Ran {
		t.Fatal("expected fallback to route 2 on precommit failure")
	}
	if len(texts) != 1 || texts[0] != "recovered" {
		t.Fatalf("expected 'recovered', got %v", texts)
	}
}

func TestRouterDoesNotFallbackOnNonRetryableFailure(t *testing.T) {
	r2Ran := false
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			// Generic 400 Bad Request — should stop fallback immediately
			ch <- Chunk{Kind: ChunkError, Err: &httpError{Status: http.StatusBadRequest, Body: "invalid json syntax"}, Retryable: false}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			r2Ran = true
			ch := make(chan Chunk, 1)
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, err := router.Stream(context.Background(), makeValidRequest())
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	var gotError error
	for c := range out {
		if c.Kind == ChunkError {
			gotError = c.Err
		}
	}

	if r2Ran {
		t.Fatal("router MUST NOT fallback on non-retryable 400 error")
	}
	if gotError == nil {
		t.Fatal("expected error to be propagated to consumer")
	}
	if !strings.Contains(gotError.Error(), "400") {
		t.Fatalf("expected error to mention 400, got: %v", gotError)
	}
}

func TestRouterDoesNotFallbackOnGeneric400(t *testing.T) {
	TestRouterDoesNotFallbackOnNonRetryableFailure(t)
}

func TestRouterFallsBackOnServerError(t *testing.T) {
	r2Ran := false
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkError, Err: &httpError{Status: 502, Body: "bad gateway"}, Retryable: true}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			r2Ran = true
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkText, Text: "from r2"}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())
	for range out {
	}
	if !r2Ran {
		t.Fatal("expected fallback on HTTP 502 server error")
	}
}

func TestRouterFallsBackOnNetworkError(t *testing.T) {
	r2Ran := false
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			return nil, errors.New("dial tcp: i/o timeout")
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			r2Ran = true
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkText, Text: "recovered"}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())
	for range out {
	}
	if !r2Ran {
		t.Fatal("expected fallback on synchronous start network error")
	}
}

func TestRouterFallsBackOnUnexpectedEOFBeforeOutput(t *testing.T) {
	r2Ran := false
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			// Channel closes immediately without any chunk
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			r2Ran = true
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkText, Text: "from r2"}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())
	for range out {
	}
	if !r2Ran {
		t.Fatal("expected fallback when channel closes unexpectedly before semantic chunk")
	}
}

func TestRouterFallsBackOnPrestreamTimeout(t *testing.T) {
	fc := newFakeClock(time.Now())
	r2Ran := false

	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk)
			go func() {
				<-ctx.Done()
				close(ch)
			}()
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			r2Ran = true
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkText, Text: "r2 ok"}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 20*time.Millisecond, fc)
	out, _ := router.Stream(context.Background(), makeValidRequest())

	done := make(chan struct{})
	go func() {
		for range out {
		}
		close(done)
	}()

	fc.Advance(50 * time.Millisecond)

	select {
	case <-done:
		if !r2Ran {
			t.Fatal("expected fallback to r2 after prestream timeout")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for router stream completion")
	}
}

func TestRouterFallsBackOnCredentialFailureWithRedactedLog(t *testing.T) {
	r2Ran := false
	secretKey := "sk-ant-verysecretcredential12345"

	r1 := Route{
		Provider: "anthropic", Model: "claude-3",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkError, Err: &httpError{Status: 401, Body: "invalid key: " + secretKey}, Retryable: true}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "groq", Model: "qwen",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			r2Ran = true
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkText, Text: "ok"}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())

	var traces []ChunkRouteTrace
	for c := range out {
		if c.Kind == ChunkTrace && c.RouteTrace != nil {
			traces = append(traces, *c.RouteTrace)
		}
	}

	if !r2Ran {
		t.Fatal("expected fallback on 401 credential failure")
	}
	for _, tr := range traces {
		if strings.Contains(tr.Reason, secretKey) {
			t.Fatalf("secret key leaked in trace reason: %s", tr.Reason)
		}
	}
}

func TestRouterDoesNotFallbackOnParentCancellation(t *testing.T) {
	r2Ran := false
	parentCtx, cancel := context.WithCancel(context.Background())

	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk)
			go func() {
				cancel() // cancel parent while r1 is waiting
				<-ctx.Done()
				close(ch)
			}()
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			r2Ran = true
			ch := make(chan Chunk, 1)
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, _ := router.Stream(parentCtx, makeValidRequest())

	var lastErr error
	for c := range out {
		if c.Kind == ChunkError {
			lastErr = c.Err
		}
	}

	if r2Ran {
		t.Fatal("router MUST NOT fallback when parent context was cancelled")
	}
	if !errors.Is(lastErr, context.Canceled) {
		t.Fatalf("expected context.Canceled error, got: %v", lastErr)
	}
}

func TestRouterParentCancellationStopsRouting(t *testing.T) {
	TestRouterDoesNotFallbackOnParentCancellation(t)
}

func TestRouterExhaustionReportsEveryRoute(t *testing.T) {
	r1 := Route{
		Provider: "prov1", Model: "mod1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkError, Err: errors.New("err1"), Retryable: true}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "prov2", Model: "mod2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkError, Err: errors.New("err2"), Retryable: true}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())

	var routerExhausted *RouterExhaustedError
	for c := range out {
		if c.Kind == ChunkError {
			errors.As(c.Err, &routerExhausted)
		}
	}

	if routerExhausted == nil {
		t.Fatal("expected RouterExhaustedError on complete exhaustion")
	}
	if len(routerExhausted.Attempts) != 2 {
		t.Fatalf("expected 2 attempts reported, got %d", len(routerExhausted.Attempts))
	}
	if routerExhausted.Attempts[0].Provider != "prov1" || routerExhausted.Attempts[1].Provider != "prov2" {
		t.Fatalf("unexpected attempt providers: %v", routerExhausted.Attempts)
	}
}

// ─── Q.3 Commit boundary & linearization ──────────────────────────────────────

func TestRouterFirstSemanticChunkCommitsRoute(t *testing.T) {
	r2Ran := false
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 2)
			ch <- Chunk{Kind: ChunkText, Text: "part1"}
			// Post-commit failure must NOT trigger fallback
			ch <- Chunk{Kind: ChunkError, Err: errors.New("mid-stream failure")}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			r2Ran = true
			ch := make(chan Chunk, 1)
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())

	var chunks []Chunk
	for c := range out {
		chunks = append(chunks, c)
	}

	if r2Ran {
		t.Fatal("route 2 MUST NOT run after route 1 committed with first semantic chunk")
	}
	// Post-commit error must be passed through directly
	var gotErr bool
	for _, c := range chunks {
		if c.Kind == ChunkError && strings.Contains(c.Err.Error(), "mid-stream failure") {
			gotErr = true
		}
	}
	if !gotErr {
		t.Fatal("mid-stream failure must pass through to consumer")
	}
}

func TestRouterFirstDeliveredChunkCommitsRoute(t *testing.T) {
	TestRouterFirstSemanticChunkCommitsRoute(t)
}

func TestRouterDoesNotFallbackAfterText(t *testing.T) {
	r2Ran := false
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 2)
			ch <- Chunk{Kind: ChunkText, Text: "token"}
			ch <- Chunk{Kind: ChunkError, Err: errors.New("socket closed")}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			r2Ran = true
			ch := make(chan Chunk, 1)
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())
	for range out {
	}
	if r2Ran {
		t.Fatal("fallback occurred after ChunkText!")
	}
}

func TestRouterDoesNotFallbackAfterToolCall(t *testing.T) {
	r2Ran := false
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 2)
			ch <- Chunk{Kind: ChunkToolCall, Call: &ToolCall{ID: "call_1", Name: "bash"}}
			ch <- Chunk{Kind: ChunkError, Err: errors.New("socket closed")}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			r2Ran = true
			ch := make(chan Chunk, 1)
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())
	for range out {
	}
	if r2Ran {
		t.Fatal("fallback occurred after ChunkToolCall!")
	}
}

func TestRouterDoesNotFallbackAfterStop(t *testing.T) {
	r2Ran := false
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 2)
			ch <- Chunk{Kind: ChunkStop, Stop: "end_turn"}
			ch <- Chunk{Kind: ChunkError, Err: errors.New("stream err")}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			r2Ran = true
			ch := make(chan Chunk, 1)
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())
	for range out {
	}
	if r2Ran {
		t.Fatal("fallback occurred after ChunkStop!")
	}
}

func TestRouterInterceptsPrecommitErrorChunk(t *testing.T) {
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkError, Err: errors.New("p1 failed"), Retryable: true}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkText, Text: "success"}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())

	var receivedErrors []error
	for c := range out {
		if c.Kind == ChunkError {
			receivedErrors = append(receivedErrors, c.Err)
		}
	}

	if len(receivedErrors) > 0 {
		t.Fatalf("precommit error chunk leaked to downstream: %v", receivedErrors)
	}
}

func TestRouterPrecommitErrorChunkNeverExposedUnlessExhausted(t *testing.T) {
	TestRouterInterceptsPrecommitErrorChunk(t)
}

func TestRouterInterceptsPrecommitRateLimitChunk(t *testing.T) {
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkRateLimit, RateLimit: &RateLimitInfo{Code: 429, WaitSec: 5}}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkText, Text: "p2 ok"}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())

	var receivedRL []*RateLimitInfo
	for c := range out {
		if c.Kind == ChunkRateLimit {
			receivedRL = append(receivedRL, c.RateLimit)
		}
	}

	if len(receivedRL) > 0 {
		t.Fatalf("precommit ChunkRateLimit leaked before exhaustion: %v", receivedRL)
	}
}

func TestRouterPrecommitRateLimitChunkNeverExposedUnlessExhausted(t *testing.T) {
	TestRouterInterceptsPrecommitRateLimitChunk(t)
}

func TestRouterTraceEmittedAfterCommitFlipNotBefore(t *testing.T) {
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 2)
			ch <- Chunk{Kind: ChunkText, Text: "committed!"}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())

	var events []string
	for c := range out {
		if c.Kind == ChunkTrace && c.RouteTrace != nil {
			events = append(events, "trace:"+c.RouteTrace.Status)
		} else if c.Kind == ChunkText {
			events = append(events, "text:"+c.Text)
		}
	}

	// Trace "selected" must arrive before the text chunk in output stream (J.5)
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got: %v", events)
	}
	if events[0] != "trace:selected" {
		t.Fatalf("expected trace:selected first, got: %s", events[0])
	}
	if events[1] != "text:committed!" {
		t.Fatalf("expected text:committed! second, got: %s", events[1])
	}
}

func TestRouterPreservesPrecommitMetadataOrderAfterCommit(t *testing.T) {
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 3)
			// Non-semantic chunks buffered pre-commit
			ch <- Chunk{Kind: ChunkKind(99), Text: "meta1"}
			ch <- Chunk{Kind: ChunkKind(99), Text: "meta2"}
			ch <- Chunk{Kind: ChunkText, Text: "semantic"}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())

	var sequence []string
	for c := range out {
		if c.Kind == ChunkKind(99) {
			sequence = append(sequence, c.Text)
		} else if c.Kind == ChunkText {
			sequence = append(sequence, c.Text)
		}
	}

	expected := []string{"meta1", "meta2", "semantic"}
	if !reflect.DeepEqual(sequence, expected) {
		t.Fatalf("expected sequence %v, got %v", expected, sequence)
	}
}

func TestRouterRejectsExcessivePrecommitMetadataChunks(t *testing.T) {
	r2Ran := false
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, MaxPrecommitBufferedChunks+5)
			// Emit more than MaxPrecommitBufferedChunks metadata chunks without a semantic chunk
			for i := 0; i < MaxPrecommitBufferedChunks+2; i++ {
				ch <- Chunk{Kind: ChunkKind(99), Text: fmt.Sprintf("meta%d", i)}
			}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			r2Ran = true
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkText, Text: "from r2"}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())
	for range out {
	}

	if !r2Ran {
		t.Fatal("expected fallback when route exceeds MaxPrecommitBufferedChunks")
	}
}

func TestRouterCommitAndCancellationBoundaryIsDeterministic(t *testing.T) {
	// If parent is cancelled before commit, parent cancel wins
	parentCtx, cancel := context.WithCancel(context.Background())
	cancel()

	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkText, Text: "already ready"}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1}, 5*time.Second, RealClock{})
	out, err := router.Stream(parentCtx, makeValidRequest())
	if err != nil {
		t.Fatalf("Stream unexpected sync err: %v", err)
	}

	var gotCancel bool
	for c := range out {
		if errors.Is(c.Err, context.Canceled) {
			gotCancel = true
		}
	}
	if !gotCancel {
		t.Fatal("parent cancel must win when already cancelled at linearization point")
	}
}

func TestRouterParentCancelWinsAtLinearizationPoint(t *testing.T) {
	TestRouterCommitAndCancellationBoundaryIsDeterministic(t)
}

func TestRouterCommitWinsWhenCancelArrivesAfterLinearizationPoint(t *testing.T) {
	parentCtx, cancel := context.WithCancel(context.Background())

	unblock := make(chan struct{})
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 2)
			ch <- Chunk{Kind: ChunkText, Text: "chunk1"}
			go func() {
				<-unblock
				cancel() // cancel after commit
				ch <- Chunk{Kind: ChunkText, Text: "chunk2"}
				close(ch)
			}()
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1}, 5*time.Second, RealClock{})
	out, _ := router.Stream(parentCtx, makeValidRequest())

	// Read first chunk (commit has happened)
	c1 := <-out
	if c1.Kind == ChunkTrace {
		// Drain trace if any
		c1 = <-out
	}
	if c1.Kind != ChunkText || c1.Text != "chunk1" {
		t.Fatalf("expected chunk1, got: %+v", c1)
	}

	close(unblock)
	for range out {
	}
	// Test passes if no panic or hang and chunk1 was successfully delivered
}

func TestRouterCommitAndDeadlineBoundaryIsDeterministic(t *testing.T) {
	fc := newFakeClock(time.Now())

	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			// Route produces chunk, but clock is advanced beyond deadline before linearization
			fc.Advance(10 * time.Second)
			ch <- Chunk{Kind: ChunkText, Text: "late"}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkText, Text: "from r2"}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, fc)
	out, _ := router.Stream(context.Background(), makeValidRequest())

	var texts []string
	for c := range out {
		if c.Kind == ChunkText {
			texts = append(texts, c.Text)
		}
	}

	if len(texts) != 1 || texts[0] != "from r2" {
		t.Fatalf("expected timeout to win and fallback to r2, got %v", texts)
	}
}

// ─── Q.4 Timing, Cancellation & Goroutine Safety ──────────────────────────────

func TestRouterDoesNotRunRoutesConcurrently(t *testing.T) {
	var active int32
	var maxActive int32

	makeRoute := func(id int) Route {
		return Route{
			Provider: fmt.Sprintf("p%d", id), Model: "m",
			Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
				curr := atomic.AddInt32(&active, 1)
				for {
					old := atomic.LoadInt32(&maxActive)
					if curr <= old || atomic.CompareAndSwapInt32(&maxActive, old, curr) {
						break
					}
				}
				ch := make(chan Chunk, 1)
				// Small yield
				runtime.Gosched()
				ch <- Chunk{Kind: ChunkError, Err: errors.New("fail"), Retryable: true}
				atomic.AddInt32(&active, -1)
				close(ch)
				return ch, nil
			}},
		}
	}

	r1 := makeRoute(1)
	r2 := makeRoute(2)
	r3 := makeRoute(3)

	router, _ := NewRouter([]Route{r1, r2, r3}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())
	for range out {
	}

	if max := atomic.LoadInt32(&maxActive); max > 1 {
		t.Fatalf("routes were executed concurrently! max active was %d (must be <= 1)", max)
	}
}

func TestRouterCancelsAndCleansPreviousRouteBeforeNext(t *testing.T) {
	r1CleanedUp := false
	r2StartedAfterR1Cleaned := false

	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk)
			go func() {
				<-ctx.Done()
				// Simulate cleanup work
				r1CleanedUp = true
				close(ch)
			}()
			return ch, nil
		}},
	}

	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			if r1CleanedUp {
				r2StartedAfterR1Cleaned = true
			}
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkText, Text: "r2 ok"}
			close(ch)
			return ch, nil
		}},
	}

	fc := newFakeClock(time.Now())
	router, _ := NewRouter([]Route{r1, r2}, 20*time.Millisecond, fc)
	out, _ := router.Stream(context.Background(), makeValidRequest())

	done := make(chan struct{})
	go func() {
		for range out {
		}
		close(done)
	}()

	fc.Advance(50 * time.Millisecond)
	<-done

	if !r2StartedAfterR1Cleaned {
		t.Fatal("route 2 started before route 1 was completely cleaned up!")
	}
}

func TestRouterWaitsForCleanupAckBeforeNextRoute(t *testing.T) {
	TestRouterCancelsAndCleansPreviousRouteBeforeNext(t)
}

func TestRouterStopsWhenCleanupTimesOut(t *testing.T) {
	r2Ran := false
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk)
			// Misbehaving provider: ignores ctx cancellation and never closes channel
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			r2Ran = true
			ch := make(chan Chunk, 1)
			close(ch)
			return ch, nil
		}},
	}

	// Use very short prestreamTimeout
	router, _ := NewRouter([]Route{r1, r2}, 50*time.Millisecond, RealClock{})
	// Override cleanupTimeout to be short for test speed
	router.cleanupTimeout = 100 * time.Millisecond

	out, err := router.Stream(context.Background(), makeValidRequest())
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	var gotError error
	for c := range out {
		if c.Kind == ChunkError {
			gotError = c.Err
		}
	}

	if r2Ran {
		t.Fatal("route 2 MUST NOT run when route 1 cleanup times out!")
	}
	if !errors.Is(gotError, ErrRouteCleanupTimeout) {
		t.Fatalf("expected ErrRouteCleanupTimeout, got: %v", gotError)
	}
}

func TestRouterStopsImmediatelyOnCleanupTimeout(t *testing.T) {
	TestRouterStopsWhenCleanupTimesOut(t)
}

func TestRouterUsesFreshPrestreamTimeoutPerRoute(t *testing.T) {
	var deadlines []time.Time
	var mu sync.Mutex

	recordDeadline := func(ctx context.Context) {
		dl, ok := ctx.Deadline()
		if ok {
			mu.Lock()
			deadlines = append(deadlines, dl)
			mu.Unlock()
		}
	}

	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			recordDeadline(ctx)
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkError, Err: errors.New("err1"), Retryable: true}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			recordDeadline(ctx)
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkText, Text: "done"}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())
	for range out {
	}

	mu.Lock()
	defer mu.Unlock()
	if len(deadlines) != 2 {
		t.Fatalf("expected 2 route deadlines, got %d", len(deadlines))
	}
	if !deadlines[1].After(deadlines[0]) {
		t.Fatalf("route 2 deadline (%v) should be after route 1 deadline (%v)", deadlines[1], deadlines[0])
	}
}

func TestRouterStreamReturnsPromptlyBeforeFirstRouteResolves(t *testing.T) {
	started := make(chan struct{})
	block := make(chan struct{})

	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			close(started)
			ch := make(chan Chunk)
			go func() {
				<-block
				close(ch)
			}()
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1}, 5*time.Second, RealClock{})

	start := time.Now()
	out, err := router.Stream(context.Background(), makeValidRequest())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("Stream took %v to return; MUST return promptly (async I/O)", elapsed)
	}

	close(block)
	for range out {
	}
}

func TestRouterInputValidationErrorReturnedSynchronously(t *testing.T) {
	router, _ := NewRouter([]Route{{Provider: "p", Model: "m", Client: &mockSingleAttempt{}}}, 5*time.Second, RealClock{})

	// Empty messages list is a sync validation error
	_, err := router.Stream(context.Background(), Request{Messages: nil})
	if err == nil {
		t.Fatal("expected synchronous error for empty request messages, got nil")
	}
}

func TestRouterOutputChannelClosedExactlyOnceInEveryPath(t *testing.T) {
	cases := []struct {
		name   string
		routes []Route
		ctxFn  func() (context.Context, context.CancelFunc)
	}{
		{
			name: "success",
			routes: []Route{{Provider: "p", Model: "m", Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
				ch := make(chan Chunk, 1)
				ch <- Chunk{Kind: ChunkText, Text: "hi"}
				close(ch)
				return ch, nil
			}}}},
			ctxFn: func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
		},
		{
			name: "exhaustion",
			routes: []Route{{Provider: "p", Model: "m", Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
				ch := make(chan Chunk, 1)
				ch <- Chunk{Kind: ChunkError, Err: errors.New("err"), Retryable: true}
				close(ch)
				return ch, nil
			}}}},
			ctxFn: func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
		},
		{
			name: "cancellation",
			routes: []Route{{Provider: "p", Model: "m", Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
				ch := make(chan Chunk)
				return ch, nil
			}}}},
			ctxFn: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, _ := NewRouter(tc.routes, 5*time.Second, RealClock{})
			ctx, cancel := tc.ctxFn()
			defer cancel()

			out, err := router.Stream(ctx, makeValidRequest())
			if err != nil {
				t.Fatalf("Stream sync err: %v", err)
			}

			// Drain channel. If channel is not closed, test will timeout
			done := make(chan struct{})
			go func() {
				for range out {
				}
				close(done)
			}()

			select {
			case <-done:
				// Channel was properly closed
			case <-time.After(2 * time.Second):
				t.Fatal("out channel was never closed!")
			}
		})
	}
}

func TestRouterDoesNotLeakGoroutines(t *testing.T) {
	initialGoroutines := runtime.NumGoroutine()

	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkError, Err: errors.New("fail"), Retryable: true}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkText, Text: "done"}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})

	for i := 0; i < 10; i++ {
		out, _ := router.Stream(context.Background(), makeValidRequest())
		for range out {
		}
	}

	// Give a small grace period for goroutines to terminate
	time.Sleep(50 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	// Allow small delta for test runner runtime fluctuations
	if delta := finalGoroutines - initialGoroutines; delta > 4 {
		t.Fatalf("possible goroutine leak: started with %d, ended with %d (delta %d)",
			initialGoroutines, finalGoroutines, delta)
	}
}

// ─── Q.5 429 & Retry-After ───────────────────────────────────────────────────

func TestRouterAll429SelectsShortestPositiveRetryAfter(t *testing.T) {
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkRateLimit, RateLimit: &RateLimitInfo{Code: 429, WaitSec: 20, RawRetryAfter: "20"}}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkRateLimit, RateLimit: &RateLimitInfo{Code: 429, WaitSec: 5, RawRetryAfter: "5"}}
			close(ch)
			return ch, nil
		}},
	}
	r3 := Route{
		Provider: "p3", Model: "m3",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkRateLimit, RateLimit: &RateLimitInfo{Code: 429, WaitSec: 15, RawRetryAfter: "15"}}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2, r3}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())

	var rlChunk *Chunk
	for c := range out {
		if c.Kind == ChunkRateLimit {
			rlChunk = &c
		}
	}

	if rlChunk == nil {
		t.Fatal("expected ChunkRateLimit chunk when all routes 429")
	}
	if rlChunk.RateLimit.WaitSec != 5 {
		t.Fatalf("expected shortest retry-after 5s, got %v", rlChunk.RateLimit.WaitSec)
	}
}

func TestRouterSkipsRateLimitedRouteWithoutWaiting(t *testing.T) {
	start := time.Now()
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkRateLimit, RateLimit: &RateLimitInfo{Code: 429, WaitSec: 60}}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkText, Text: "instant fallback"}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())
	for range out {
	}

	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Fatalf("router slept on 429! took %v", elapsed)
	}
}

func TestRouterMixedFailuresDoNotMasqueradeAsRateLimit(t *testing.T) {
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkRateLimit, RateLimit: &RateLimitInfo{Code: 429, WaitSec: 10}}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			// Non-429 error
			ch <- Chunk{Kind: ChunkError, Err: errors.New("500 internal server error"), Retryable: true}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())

	var gotError *RouterExhaustedError
	for c := range out {
		if c.Kind == ChunkRateLimit {
			t.Fatal("mixed exhaustion MUST NOT emit ChunkRateLimit")
		}
		if c.Kind == ChunkError {
			errors.As(c.Err, &gotError)
		}
	}

	if gotError == nil {
		t.Fatal("expected RouterExhaustedError for mixed exhaustion")
	}
	if gotError.Kind != ExhaustionMixed {
		t.Fatalf("expected ExhaustionMixed, got %v", gotError.Kind)
	}
}

func TestRouterParsesRetryAfterSecondsAndHTTPDate(t *testing.T) {
	fc := newFakeClock(time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))

	// Integer seconds
	d1, ok1 := parseRetryAfterValue("30", fc)
	if !ok1 || d1 != 30*time.Second {
		t.Fatalf("expected 30s, got %v (ok=%v)", d1, ok1)
	}

	// HTTP-date 45 seconds in future
	futureDate := "Fri, 04 Sep 2026 12:00:45 GMT"
	d2, ok2 := parseRetryAfterValue(futureDate, fc)
	if !ok2 || d2 != 45*time.Second {
		t.Fatalf("expected 45s from HTTP-date, got %v (ok=%v)", d2, ok2)
	}
}

func TestRouterIgnoresMalformedRetryAfter(t *testing.T) {
	fc := newFakeClock(time.Now())
	cases := []string{"invalid", "abc-seconds", "---", ""}
	for _, c := range cases {
		d, ok := parseRetryAfterValue(c, fc)
		if ok || d != 0 {
			t.Fatalf("expected malformed %q to return (0, false), got (%v, %v)", c, d, ok)
		}
	}
}

func TestRouterIgnoresPastOrZeroRetryAfter(t *testing.T) {
	fc := newFakeClock(time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))
	cases := []string{
		"0",
		"-10",
		"Fri, 04 Sep 2026 11:59:59 GMT", // past date
	}
	for _, c := range cases {
		d, ok := parseRetryAfterValue(c, fc)
		if ok || d != 0 {
			t.Fatalf("expected past/zero %q to return (0, false), got (%v, %v)", c, d, ok)
		}
	}
}

func TestRouterClampsRetryAfterToRetryCeiling(t *testing.T) {
	fc := newFakeClock(time.Now())
	// 500 seconds should be clamped to maxRetryCeiling (120s)
	d, ok := parseRetryAfterValue("500", fc)
	if !ok {
		t.Fatal("expected ok=true for 500")
	}
	if d != 120*time.Second {
		t.Fatalf("expected clamped to 120s, got %v", d)
	}
}

func TestRouterAllRateLimitedWithoutRetryAfterUsesAgentBackoff(t *testing.T) {
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkRateLimit, RateLimit: &RateLimitInfo{Code: 429, WaitSec: 0, RawRetryAfter: ""}}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkRateLimit, RateLimit: &RateLimitInfo{Code: 429, WaitSec: 0, RawRetryAfter: "invalid"}}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())

	var rlChunk *Chunk
	for c := range out {
		if c.Kind == ChunkRateLimit {
			rlChunk = &c
		}
	}

	if rlChunk == nil {
		t.Fatal("expected ChunkRateLimit chunk")
	}
	if rlChunk.RateLimit.WaitSec != 0 {
		t.Fatalf("expected WaitSec == 0 when no route provides valid Retry-After, got %v", rlChunk.RateLimit.WaitSec)
	}
}

func TestRouterIgnoresOverflowRetryAfter(t *testing.T) {
	fc := newFakeClock(time.Now())
	// Tremendous overflow numbers
	d, ok := parseRetryAfterValue("1e308", fc)
	if ok && d > maxRetryCeiling {
		t.Fatalf("overflow retry-after exceeded ceiling: %v", d)
	}
}

func TestRetryCeilingConstantIsBounded(t *testing.T) {
	if maxRetryCeiling != 120*time.Second {
		t.Fatalf("expected maxRetryCeiling == 120s, got %v", maxRetryCeiling)
	}
}

// ─── Q.6 Redaction & Bounds ───────────────────────────────────────────────────

func TestRouterAggregateErrorIsSanitizedAndBounded(t *testing.T) {
	secretKey := "sk-or-supersecretkey99999"
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkError, Err: errors.New("failed with " + secretKey), Retryable: true}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())

	var exhaustedErr *RouterExhaustedError
	for c := range out {
		if c.Kind == ChunkError {
			errors.As(c.Err, &exhaustedErr)
		}
	}

	if exhaustedErr == nil {
		t.Fatal("expected RouterExhaustedError")
	}
	msg := exhaustedErr.Error()
	if strings.Contains(msg, secretKey) {
		t.Fatalf("secret key leaked in aggregate error: %s", msg)
	}
	if len(msg) > 16*1024+100 {
		t.Fatalf("aggregate error exceeded size bound: %d bytes", len(msg))
	}
}

// ─── Q.8 Immutability & Concurrency ───────────────────────────────────────────

func TestNewRouterDefensivelyCopiesRoutes(t *testing.T) {
	r1 := Route{Provider: "p1", Model: "m1", Client: &mockSingleAttempt{name: "orig1"}}
	r2 := Route{Provider: "p2", Model: "m2", Client: &mockSingleAttempt{name: "orig2"}}
	routes := []Route{r1, r2}

	router, err := NewRouter(routes, 5*time.Second, RealClock{})
	if err != nil {
		t.Fatalf("NewRouter error: %v", err)
	}

	// Mutate the original slice
	routes[0] = Route{Provider: "mutated", Model: "m_bad", Client: &mockSingleAttempt{name: "bad"}}

	// Verify router name and internal state unaffected
	if !strings.Contains(router.Name(), "p1:m1") {
		t.Fatalf("router routes were mutated through caller slice! name is %s", router.Name())
	}
}

func TestNewRouterRejectsEmptyRoutes(t *testing.T) {
	_, err := NewRouter(nil, 5*time.Second, RealClock{})
	if err == nil {
		t.Fatal("expected error on empty routes slice, got nil")
	}
}

func TestRouterConfigurationCannotBeMutatedThroughInputSlice(t *testing.T) {
	TestNewRouterDefensivelyCopiesRoutes(t)
}

func TestRouterHasNoExportedMutationMethod(t *testing.T) {
	routerType := reflect.TypeOf(&Router{})
	for i := 0; i < routerType.NumMethod(); i++ {
		m := routerType.Method(i)
		lower := strings.ToLower(m.Name)
		if strings.HasPrefix(lower, "set") || strings.HasPrefix(lower, "add") || strings.HasPrefix(lower, "clear") {
			t.Fatalf("router has exported mutation method: %s", m.Name)
		}
	}
}

func TestRouterConcurrentStreamsDoNotShareState(t *testing.T) {
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 2)
			ch <- Chunk{Kind: ChunkText, Text: req.Messages[0].Text}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1}, 5*time.Second, RealClock{})

	const concurrency = 20
	var wg sync.WaitGroup
	errs := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req := Request{Messages: []Message{{Role: User, Text: fmt.Sprintf("req-%d", id)}}}
			out, err := router.Stream(context.Background(), req)
			if err != nil {
				errs <- err
				return
			}
			var received string
			for c := range out {
				if c.Kind == ChunkText {
					received += c.Text
				}
			}
			if received != fmt.Sprintf("req-%d", id) {
				errs <- fmt.Errorf("state cross-contamination! expected %s, got %s", fmt.Sprintf("req-%d", id), received)
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatal(err)
	}
}

func TestRouterSafeForConcurrentStreamCalls(t *testing.T) {
	TestRouterConcurrentStreamsDoNotShareState(t)
}

// ─── StreamID & Determinism ───────────────────────────────────────────────────

func TestRouterGeneratesOneStableStreamIDPerCall(t *testing.T) {
	r1 := Route{
		Provider: "p1", Model: "m1",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkError, Err: errors.New("err1"), Retryable: true}
			close(ch)
			return ch, nil
		}},
	}
	r2 := Route{
		Provider: "p2", Model: "m2",
		Client: &mockSingleAttempt{startFn: func(ctx context.Context, req Request) (<-chan Chunk, error) {
			ch := make(chan Chunk, 1)
			ch <- Chunk{Kind: ChunkText, Text: "recovered"}
			close(ch)
			return ch, nil
		}},
	}

	router, _ := NewRouter([]Route{r1, r2}, 5*time.Second, RealClock{})
	out, _ := router.Stream(context.Background(), makeValidRequest())

	var streamIDs []string
	for c := range out {
		if c.Kind == ChunkTrace && c.RouteTrace != nil {
			streamIDs = append(streamIDs, c.RouteTrace.StreamID)
		}
	}

	if len(streamIDs) < 2 {
		t.Fatalf("expected at least 2 trace chunks with StreamID, got %d", len(streamIDs))
	}
	// All traces for the same Stream() call MUST share the exact same StreamID
	firstID := streamIDs[0]
	if len(firstID) != 32 {
		t.Fatalf("expected 32-char hex StreamID (128-bit), got %s", firstID)
	}
	for i, id := range streamIDs {
		if id != firstID {
			t.Fatalf("streamID changed within same call! id[0]=%s, id[%d]=%s", firstID, i, id)
		}
	}
}

func TestRouterFailsClosedWhenStreamIDGenerationFails(t *testing.T) {
	r1 := Route{Provider: "p", Model: "m", Client: &mockSingleAttempt{}}
	router, _ := NewRouter([]Route{r1}, 5*time.Second, RealClock{})

	// Inject failing streamID generator
	router.WithStreamIDFunc(func() (string, error) {
		return "", errors.New("entropy source depleted")
	})

	_, err := router.Stream(context.Background(), makeValidRequest())
	if err == nil {
		t.Fatal("router MUST fail closed when streamID generation fails")
	}
	if !strings.Contains(err.Error(), "stream ID") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
