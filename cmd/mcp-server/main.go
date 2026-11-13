package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/amiraq1/nabd/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get cwd: %v\n", err)
		os.Exit(1)
	}

	reg, err := tools.NewRegistry(cwd, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create registry: %v\n", err)
		os.Exit(1)
	}
	// This server has no interactive approver. Registry.Run refuses every tool
	// that is not read-only, and mcp_call (arbitrary process execution) is
	// removed entirely.
	reg.Disable("mcp_call")

	s := server.NewMCPServer(
		"NabdCoder Tools",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	// Register tools from the registry
	for _, toolDef := range reg.OpenAITools() {
		fn := toolDef["function"].(map[string]any)
		name := fn["name"].(string)
		desc := fn["description"].(string)

		tool := mcp.NewTool(name, mcp.WithDescription(desc))
		s.AddTool(tool, makeHandler(reg, name))
	}

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}

func makeHandler(reg *tools.Registry, name string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := json.Marshal(request.GetArguments())
		result := reg.Run(ctx, name, args)
		return mcp.NewToolResultText(result), nil
	}
}
