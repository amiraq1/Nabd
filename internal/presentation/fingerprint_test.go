package presentation

import (
	"testing"

	"nabd/internal/agent"
)

// TestFingerprintDeterministic verifies identical items produce identical fingerprints.
func TestFingerprintDeterministic(t *testing.T) {
	items := []FeedItem{
		{Type: ItemUserMsg, Text: "hello"},
		{Type: ItemAssistant, Text: "world"},
		{Type: ItemTool, Tool: &ToolCard{Name: "bash", Status: ToolDone, Args: "ls -la"}},
		{Type: ItemPermission, Perm: &PermCard{Name: "write_file", Status: PermAllow}},
		{Type: ItemNotice, Text: "a note"},
		{Type: ItemError, Text: "something failed"},
		{Type: ItemRunBoundary, RunBoundary: "start"},
	}

	for _, it := range items {
		first := it.Fingerprint()
		second := it.Fingerprint()
		if first != second {
			t.Fatalf("fingerprint not deterministic for %s: %d != %d", it.Type, first, second)
		}
	}
}

// TestFingerprintFieldSensitivity verifies that changing each visible field
// changes the fingerprint.
func TestFingerprintFieldSensitivity(t *testing.T) {
	base := FeedItem{Type: ItemUserMsg, Text: "hello"}

	baseFP := base.Fingerprint()

	changed := base
	changed.Type = ItemAssistant
	if changed.Fingerprint() == baseFP {
		t.Fatal("changing Type did not change fingerprint")
	}

	changed = base
	changed.Text = "world"
	if changed.Fingerprint() == baseFP {
		t.Fatal("changing Text did not change fingerprint")
	}

	changed = base
	changed.RunBoundary = "start"
	if changed.Fingerprint() == baseFP {
		t.Fatal("changing RunBoundary did not change fingerprint")
	}
}

// TestFingerprintToolSensitivity verifies all ToolCard fields affect the fingerprint.
func TestFingerprintToolSensitivity(t *testing.T) {
	base := FeedItem{
		Type: ItemTool,
		Tool: &ToolCard{
			Name:      "bash",
			Args:      "ls",
			Status:    ToolRunning,
			Output:    "output",
			Duration:  100,
			ExitCode:  0,
			Signal:    "",
			Err:       "",
			Truncated: false,
		},
	}

	baseFP := base.Fingerprint()

	cases := []struct {
		name string
		mod  func(*ToolCard)
	}{
		{"Name", func(t *ToolCard) { t.Name = "sh" }},
		{"Args", func(t *ToolCard) { t.Args = "pwd" }},
		{"Status", func(t *ToolCard) { t.Status = ToolDone }},
		{"Output", func(t *ToolCard) { t.Output = "different" }},
		{"Duration", func(t *ToolCard) { t.Duration = 200 }},
		{"ExitCode", func(t *ToolCard) { t.ExitCode = 1 }},
		{"Signal", func(t *ToolCard) { t.Signal = "SIGKILL" }},
		{"Err", func(t *ToolCard) { t.Err = "failed" }},
		{"Truncated", func(t *ToolCard) { t.Truncated = true }},
	}

	for _, tc := range cases {
		changed := base
		toolCopy := *base.Tool
		tc.mod(&toolCopy)
		changed.Tool = &toolCopy
		if changed.Fingerprint() == baseFP {
			t.Fatalf("changing Tool.%s did not change fingerprint", tc.name)
		}
	}
}

// TestFingerprintPermSensitivity verifies all PermCard fields affect the fingerprint.
func TestFingerprintPermSensitivity(t *testing.T) {
	base := FeedItem{
		Type: ItemPermission,
		Perm: &PermCard{
			Name:      "write_file",
			Args:      "/path",
			Status:    PermAsked,
			Decision:  agent.AllowSession,
			Effective: agent.AllowSession,
		},
	}

	baseFP := base.Fingerprint()

	cases := []struct {
		name string
		mod  func(*PermCard)
	}{
		{"Name", func(p *PermCard) { p.Name = "read_file" }},
		{"Args", func(p *PermCard) { p.Args = "/other" }},
		{"Status", func(p *PermCard) { p.Status = PermAllow }},
		{"Decision", func(p *PermCard) { p.Decision = agent.Deny }},
		{"Effective", func(p *PermCard) { p.Effective = agent.Deny }},
	}

	for _, tc := range cases {
		changed := base
		permCopy := *base.Perm
		tc.mod(&permCopy)
		changed.Perm = &permCopy
		if changed.Fingerprint() == baseFP {
			t.Fatalf("changing Perm.%s did not change fingerprint", tc.name)
		}
	}
}

// TestFingerprintInvisibleFields verifies that invisible fields (Seq, ID) do NOT
// change the fingerprint.
func TestFingerprintInvisibleFields(t *testing.T) {
	base := FeedItem{
		Type: ItemUserMsg,
		ID:   "msg-1",
		Seq:  42,
		Text: "hello",
	}

	baseFP := base.Fingerprint()

	changed := base
	changed.Seq = 999
	if changed.Fingerprint() != baseFP {
		t.Fatal("changing Seq changed fingerprint (should be invisible)")
	}

	changed = base
	changed.ID = "msg-different"
	if changed.Fingerprint() != baseFP {
		t.Fatal("changing ID changed fingerprint (should be invisible)")
	}
}

// TestFingerprintMultiLineText verifies multi-line text items are covered.
func TestFingerprintMultiLineText(t *testing.T) {
	singleLine := FeedItem{Type: ItemNotice, Text: "single line"}
	multiLine := FeedItem{Type: ItemNotice, Text: "line 1\nline 2\nline 3"}

	singleFP := singleLine.Fingerprint()
	multiFP := multiLine.Fingerprint()

	if singleFP == multiFP {
		t.Fatal("single-line and multi-line produced same fingerprint")
	}

	altMulti := FeedItem{Type: ItemNotice, Text: "line 1\nline 2\nline X"}
	if multiFP == altMulti.Fingerprint() {
		t.Fatal("different multi-line content produced same fingerprint")
	}
}

// TestFingerprintLengthPrefixing verifies that the encoding prevents boundary ambiguity.
func TestFingerprintLengthPrefixing(t *testing.T) {
	item1 := FeedItem{
		Type: ItemTool,
		Tool: &ToolCard{Name: "ab", Args: "c"},
	}
	item2 := FeedItem{
		Type: ItemTool,
		Tool: &ToolCard{Name: "a", Args: "bc"},
	}

	if item1.Fingerprint() == item2.Fingerprint() {
		t.Fatal("length-prefixing failed: [ab,c] collided with [a,bc]")
	}
}

// TestFingerprintNilVsEmpty verifies nil vs empty pointer behavior.
func TestFingerprintNilVsEmpty(t *testing.T) {
	nilTool := FeedItem{Type: ItemTool, Tool: nil}
	emptyTool := FeedItem{Type: ItemTool, Tool: &ToolCard{}}

	if nilTool.Fingerprint() == emptyTool.Fingerprint() {
		t.Fatal("nil Tool and empty Tool produced same fingerprint")
	}

	nilPerm := FeedItem{Type: ItemPermission, Perm: nil}
	emptyPerm := FeedItem{Type: ItemPermission, Perm: &PermCard{}}

	if nilPerm.Fingerprint() == emptyPerm.Fingerprint() {
		t.Fatal("nil Perm and empty Perm produced same fingerprint")
	}
}
