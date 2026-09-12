package provider

import (
	"context"
	"testing"
	"time"
)

type breakerTestClient struct{}

func (breakerTestClient) Name() string { return "breaker-test" }
func (breakerTestClient) Start(context.Context, Request) (<-chan Chunk, error) {
	return nil, nil
}

func TestRouteBreakerBlocksOnlyTrippedRoute(t *testing.T) {
	r := &Router{
		clock:           RealClock{},
		blockedUntil:    make(map[string]time.Time),
		breakerCooldown: time.Minute,
	}
	bad := Route{Provider: "anthropic", Model: "m", Client: breakerTestClient{}}
	other := Route{Provider: "groq", Model: "m", Client: breakerTestClient{}}
	if !r.routeAllowed(bad) || !r.routeAllowed(other) {
		t.Fatal("fresh routes must be allowed")
	}
	r.tripRoute(bad)
	if r.routeAllowed(bad) {
		t.Fatal("tripped route was allowed")
	}
	if !r.routeAllowed(other) {
		t.Fatal("breaker leaked to a different route")
	}
}

type breakerClock struct{ now time.Time }

func (c *breakerClock) Now() time.Time             { return c.now }
func (*breakerClock) NewTimer(time.Duration) Timer { return nil }

func TestRouteBreakerAllowsHalfOpenProbeAfterCooldown(t *testing.T) {
	clock := &breakerClock{now: time.Unix(100, 0)}
	r := &Router{
		clock:           clock,
		blockedUntil:    make(map[string]time.Time),
		breakerCooldown: time.Minute,
	}
	route := Route{Provider: "anthropic", Model: "m", Client: breakerTestClient{}}
	r.tripRoute(route)
	if r.routeAllowed(route) {
		t.Fatal("route allowed before cooldown elapsed")
	}
	clock.now = clock.now.Add(time.Minute)
	if !r.routeAllowed(route) {
		t.Fatal("route remained blocked after cooldown")
	}
}
