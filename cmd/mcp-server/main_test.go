package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amiraq1/nabd/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
)

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	for _, content := range res.Content {
		if text, ok := content.(mcp.TextContent); ok {
			return text.Text
		}
	}
	t.Fatalf("tool result has no text content: %+v", res.Content)
	return ""
}

func TestServerIsReadOnlyAndDisablesMCPCall(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(victim, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, err := tools.NewRegistry(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	reg.Disable("mcp_call")

	if reg.Has("mcp_call") {
		t.Fatal("mcp_call is still registered")
	}
	for _, def := range reg.OpenAITools() {
		fn, ok := def["function"].(map[string]any)
		if !ok {
			t.Fatalf("unexpected tool definition: %#v", def)
		}
		if fn["name"] == "mcp_call" {
			t.Fatal("mcp_call is still advertised")
		}
	}

	editReq := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "edit_file",
		Arguments: map[string]any{
			"path":     "victim.txt",
			"old_text": "original",
			"new_text": "changed",
		},
	}}
	res, err := makeHandler(reg, "edit_file")(context.Background(), editReq)
	if err != nil {
		t.Fatal(err)
	}
	if text := resultText(t, res); !strings.Contains(text, "requires approval") {
		t.Fatalf("edit_file through MCP = %q, want approval refusal", text)
	}
	if got, _ := os.ReadFile(victim); string(got) != "original" {
		t.Fatalf("MCP server modified the file without approval: %q", got)
	}

	readReq := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      "read_file",
		Arguments: map[string]any{"path": "victim.txt"},
	}}
	res, err = makeHandler(reg, "read_file")(context.Background(), readReq)
	if err != nil {
		t.Fatal(err)
	}
	if text := resultText(t, res); text != "original" {
		t.Fatalf("read_file through MCP = %q", text)
	}
}
