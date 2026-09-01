package main

import (
	"context"
	"fmt"
	"nabd/internal/agent"
	"nabd/internal/perm"
	"nabd/internal/snap"
	"nabd/internal/tools"
	"nabd/internal/provider"
)

type dummyAsker struct{}

func (dummyAsker) Ask(ctx context.Context, call agent.ToolCall) agent.Decision {
	fmt.Printf("UI Ask: %s — %s\n", call.Name, string(call.Args))
	return agent.AllowOnce
}

type mockGate struct {
	p *perm.Policy
}
func (g mockGate) Check(tool string) (agent.Verdict, string) {
	v, why := g.p.Check(tool)
	switch v {
	case perm.Allow:
		return agent.VerdictAllow, why
	case perm.Deny:
		return agent.VerdictDeny, why
	}
	return agent.VerdictAsk, why
}
func (g mockGate) Record(tool string, d agent.Decision) {
	if d == agent.AllowSession {
		g.p.Record(tool, agent.AllowSession)
	}
}

func main() {
	root, _ := tools.NewRoot("")
	sh, _ := snap.New(root.Dir())
	reg := tools.NewRegistry(root, sh)
	pol := perm.New(reg)
	g := mockGate{p: pol}
	
	// Create a tool call to write a test file
	args := []byte(`{"path":"test_allow.md","content":"allowed"}`)
	
	fmt.Println("--- Gate Check ---")
	d, why := g.Check("write_file")
	fmt.Printf("Gate Verdict: %v (Expected: 0 / VerdictAsk)\nWhy: %s\n", d, why)

	fmt.Println("\n--- Simulating User Approval ---")
	asker := dummyAsker{}
	call := agent.ToolCall{ID: "call_1", Name: "write_file", Args: args}
	decision := asker.Ask(context.Background(), call)
	fmt.Printf("User Decision: %v\n", decision)

	if decision == agent.AllowOnce {
		fmt.Println("\n--- Executing Tool ---")
		// Actually run it
		out, ok, err := reg.Run(context.Background(), provider.ToolCall{
			ID: "call_1", Name: "write_file", Input: args,
		})
		fmt.Printf("Result: ok=%v, err=%v\nOutput: %s\n", ok, err, out)
		
		fmt.Println("\n--- Pending Edits ---")
		edits := reg.Pending()
		for i, e := range edits {
			fmt.Printf("%d. %s %s\n", i+1, e.Tool, e.Rel)
		}
	}
}
