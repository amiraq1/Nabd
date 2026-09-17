package ui

import (
	"strings"
	"testing"
)

// TestSlashCommandsRegistryDefinitions verifies that all required commands are
// present and match expected properties.
func TestSlashCommandsRegistryDefinitions(t *testing.T) {
	cmds := AllSlashCommands()
	if len(cmds) != 9 {
		t.Fatalf("expected 9 commands, got %d", len(cmds))
	}

	expected := map[string]struct {
		hasArg    bool
		allowBusy bool
	}{
		"/undo":     {hasArg: true, allowBusy: false},
		"/rewind":   {hasArg: true, allowBusy: false},
		"/ctx":      {hasArg: false, allowBusy: false},
		"/compact":  {hasArg: false, allowBusy: false},
		"/edits":    {hasArg: false, allowBusy: false},
		"/help":     {hasArg: false, allowBusy: false},
		"/connect":  {hasArg: true, allowBusy: false},
		"/models":   {hasArg: true, allowBusy: false},
		"/provider": {hasArg: false, allowBusy: false},
	}

	for _, c := range cmds {
		exp, ok := expected[c.Name]
		if !ok {
			t.Errorf("unexpected command in registry: %s", c.Name)
			continue
		}
		if c.HasArg != exp.hasArg {
			t.Errorf("%s: hasArg = %v, want %v", c.Name, c.HasArg, exp.hasArg)
		}
		if c.AllowBusy != exp.allowBusy {
			t.Errorf("%s: allowBusy = %v, want %v", c.Name, c.AllowBusy, exp.allowBusy)
		}
		if c.Usage == "" || c.Description == "" {
			t.Errorf("%s: empty usage or description", c.Name)
		}
	}
}

// TestParseSlashCommand verifies parser behavior on empty, valid, invalid, and parameterized lines.
func TestParseSlashCommand(t *testing.T) {
	empty := ParseSlashCommand("")
	if empty.Valid || empty.Error == "" {
		t.Fatalf("expected error on empty line, got valid=%v", empty.Valid)
	}

	unknown := ParseSlashCommand("/notacommand")
	if unknown.Valid || !strings.Contains(unknown.Error, "unknown command") {
		t.Fatalf("expected unknown command error, got %+v", unknown)
	}

	undoDefault := ParseSlashCommand("/undo")
	if !undoDefault.Valid || undoDefault.N != 1 || undoDefault.HasN {
		t.Fatalf("expected undo default n=1, got %+v", undoDefault)
	}

	undoN := ParseSlashCommand("/undo 5")
	if !undoN.Valid || undoN.N != 5 || !undoN.HasN {
		t.Fatalf("expected undo n=5, got %+v", undoN)
	}

	rewindN := ParseSlashCommand("/rewind 3")
	if !rewindN.Valid || rewindN.N != 3 || !rewindN.HasN {
		t.Fatalf("expected rewind n=3, got %+v", rewindN)
	}

	prov := ParseSlashCommand("/provider")
	if !prov.Valid {
		t.Fatalf("expected /provider to be valid, got error: %s", prov.Error)
	}
	provArg := ParseSlashCommand("/provider extra")
	if provArg.Valid || !strings.Contains(provArg.Error, "usage: /provider") {
		t.Fatalf("expected /provider extra to fail with usage, got %+v", provArg)
	}

	modelsNoArg := ParseSlashCommand("/models")
	if modelsNoArg.Valid || !strings.Contains(modelsNoArg.Error, "usage: /models") {
		t.Fatalf("expected /models without args to fail with usage, got %+v", modelsNoArg)
	}
	modelsValid := ParseSlashCommand("/models groq")
	if !modelsValid.Valid || modelsValid.Arg != "groq" {
		t.Fatalf("expected /models groq to parse arg 'groq', got %+v", modelsValid)
	}

	connNoArg := ParseSlashCommand("/connect")
	if connNoArg.Valid || !strings.Contains(connNoArg.Error, "usage: /connect") {
		t.Fatalf("expected /connect without args to fail with usage, got %+v", connNoArg)
	}
	connValid := ParseSlashCommand("/connect groq")
	if !connValid.Valid || connValid.Arg != "groq" {
		t.Fatalf("expected /connect groq to parse arg 'groq', got %+v", connValid)
	}
	connWithKey := ParseSlashCommand("/connect groq sk-1234567890")
	if connWithKey.Valid || !strings.Contains(connWithKey.Error, "refusing a key") {
		t.Fatalf("expected /connect with key to fail refusing key, got %+v", connWithKey)
	}
	connKeyDirect := ParseSlashCommand("/connect sk-1234567890")
	if connKeyDirect.Valid || !strings.Contains(connKeyDirect.Error, "refusing a key") {
		t.Fatalf("expected /connect with key as first arg to fail refusing key, got %+v", connKeyDirect)
	}
}

// TestFilterSlashCommandsDeterministic verifies deterministic ordering:
// exact > prefix > alias > substring > alphabetical.
func TestFilterSlashCommandsDeterministic(t *testing.T) {
	// Empty or "/" returns every registered command (the menu hides none).
	all := FilterSlashCommands("/")
	if want := len(AllSlashCommands()); len(all) != want {
		t.Fatalf("expected every registered command for '/', got %d want %d", len(all), want)
	}

	// "/re" -> prefix match "/rewind"
	re := FilterSlashCommands("/re")
	if len(re) == 0 || re[0].Name != "/rewind" {
		t.Fatalf("expected /rewind first for '/re', got %+v", re)
	}

	// "/c" -> prefix match "/compact", "/connect", and "/ctx", alphabetical tie-break
	c := FilterSlashCommands("/c")
	if len(c) < 3 {
		t.Fatalf("expected at least 3 commands for '/c', got %d", len(c))
	}
	if c[0].Name != "/compact" || c[1].Name != "/connect" || c[2].Name != "/ctx" {
		t.Fatalf("expected /compact, /connect then /ctx, got %s, %s, %s", c[0].Name, c[1].Name, c[2].Name)
	}

	// Same query must produce identical ordering on repeated calls
	for i := 0; i < 5; i++ {
		res := FilterSlashCommands("/c")
		if res[0].Name != "/compact" || res[1].Name != "/connect" || res[2].Name != "/ctx" {
			t.Fatalf("non-deterministic results on iteration %d", i)
		}
	}
}
