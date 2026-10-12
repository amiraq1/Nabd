package approval

import (
	"context"
	"os"
	"testing"
)

// The negative tests below are the Task 2 RED set. Each asserts a property the
// old ctx-bool approval did not have: it was bound to nothing and never
// consumed, so it could be reused for any path, any call, any process.

func TestConsumeRequiresToken(t *testing.T) {
	if Consume(context.Background(), "a.txt") {
		t.Fatal("Consume must return false when ctx carries no token")
	}
}

func TestConsumeBindsPath(t *testing.T) {
	tok := Mint("a.txt", "call-1")
	ctx := WithToken(WithCall(context.Background(), "call-1"), tok)
	if Consume(ctx, "b.txt") {
		t.Fatal("a token bound to a.txt must be refused for b.txt")
	}
}

func TestConsumeBindsCallID(t *testing.T) {
	tok := Mint("a.txt", "call-1")
	ctx := WithToken(WithCall(context.Background(), "call-2"), tok)
	if Consume(ctx, "a.txt") {
		t.Fatal("a token bound to call-1 must be refused when ctx carries call-2")
	}
}

func TestConsumeBindsPID(t *testing.T) {
	tok := Mint("a.txt", "call-1")
	tok.pid = os.Getpid() + 1 // a token minted in another process
	ctx := WithToken(WithCall(context.Background(), "call-1"), tok)
	if Consume(ctx, "a.txt") {
		t.Fatal("a token bound to another process id must be refused")
	}
}

func TestConsumeSingleUse(t *testing.T) {
	tok := Mint("a.txt", "call-1")
	ctx := WithToken(WithCall(context.Background(), "call-1"), tok)
	if !Consume(ctx, "a.txt") {
		t.Fatal("first Consume of a matching token must succeed")
	}
	if Consume(ctx, "a.txt") {
		t.Fatal("a token must be refused on its second use")
	}
}
