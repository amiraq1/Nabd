package main

import (
	"context"
	"fmt"
	"nabd/internal/agent"
	"nabd/internal/perm"
	"nabd/internal/snap"
	"nabd/internal/tools"
)

type dummyAsker struct{}

func (dummyAsker) Ask(ctx context.Context, call agent.ToolCall) agent.Decision {
	fmt.Printf("UI Ask: %s\n", call.Name)
	return agent.Deny // Just simulate pressing 'n'
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
	
	l := &agent.Loop{
		Tools: reg,
		Gate:  g,
		Human: dummyAsker{},
	}
	// Simulate what runCalls would do
	calls := []agent.ToolCall{
		{ID: "call_1", Name: "write_file", Args: []byte(`{"path":"HELLO.md","content":"hello"}`)},
	}
	
	d, why := l.Gate.Check(calls[0].Name)
	fmt.Printf("Gate Check: %v, %s\n", d, why)
	// It should be VerdictAsk
	
	// Simulate decision flow
	decision := l.Human.Ask(context.Background(), calls[0])
	fmt.Printf("User Decision: %v\n", decision)
	if decision == agent.Deny {
		fmt.Printf("Tool Denied!\n")
	}
}
