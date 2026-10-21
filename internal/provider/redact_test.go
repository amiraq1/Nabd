package provider

import "testing"

func TestRouterSecretKeysAggregatesRoutes(t *testing.T) {
	a := &Anthropic{Key: "sk-ant-aggregated-key-1"}
	o := &OpenAICompat{Key: "sk-aggregated-key-2"}
	r, err := NewRouter([]Route{
		{Provider: "a", Model: "m1", Client: a},
		{Provider: "o", Model: "m2", Client: o},
		// Same key twice must not duplicate.
		{Provider: "a2", Model: "m3", Client: a},
	}, 0, RealClock{})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	keys := r.SecretKeys()
	if len(keys) != 2 {
		t.Fatalf("expected 2 deduplicated keys, got %v", keys)
	}
	if keys[0] != "sk-ant-aggregated-key-1" || keys[1] != "sk-aggregated-key-2" {
		t.Fatalf("unexpected key order/values: %v", keys)
	}
}

func TestExactKeysHelper(t *testing.T) {
	if got := ExactKeys(nil); got != nil {
		t.Fatalf("nil provider: got %v", got)
	}
	a := &Anthropic{Key: "sk-ant-exact-key"}
	if got := ExactKeys(a); len(got) != 1 || got[0] != "sk-ant-exact-key" {
		t.Fatalf("standalone provider: got %v", got)
	}
	if got := ExactKeys(&Anthropic{}); len(got) != 0 {
		t.Fatalf("keyless provider: got %v", got)
	}
}
