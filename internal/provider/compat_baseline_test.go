package provider

import (
	"encoding/json"
	"testing"
)

// TestOpenAIEncodingBaseline pins the pre-compat wire contract. Future compat
// flags may opt out per endpoint, but their zero values must preserve this body.
func TestOpenAIEncodingBaseline(t *testing.T) {
	o := &OpenAICompat{Model: "wire-model"}
	body, err := o.encode(Request{
		System: "system instructions",
		MaxTok: 321,
		Messages: []Message{{
			Role: User,
			Text: "after tool",
			ToolResults: []ToolResult{{ID: "call_1", Output: "result"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		MaxTokens int `json:"max_tokens"`
		StreamOptions *struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.MaxTokens != 321 {
		t.Fatalf("max_tokens = %d, want 321", got.MaxTokens)
	}
	if got.StreamOptions == nil || !got.StreamOptions.IncludeUsage {
		t.Fatal("stream_options.include_usage must default to true")
	}
	wantRoles := []string{"system", "tool", "user"}
	if len(got.Messages) != len(wantRoles) {
		t.Fatalf("message count = %d, want %d", len(got.Messages), len(wantRoles))
	}
	for i, want := range wantRoles {
		if got.Messages[i].Role != want {
			t.Errorf("message[%d].role = %q, want %q", i, got.Messages[i].Role, want)
		}
	}
}
