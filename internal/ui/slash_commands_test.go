package ui

import (
	"strings"
	"testing"
)

func TestSlashCommandsRegistryDefinitions(t *testing.T) {
	cmds := AllSlashCommands()
	if len(cmds) != 6 { t.Fatalf("expected 6 commands, got %d", len(cmds)) }
	expected := map[string]struct{ hasArg, allowBusy bool }{
		"/undo": {true, false}, "/rewind": {true, false}, "/ctx": {false, false},
		"/compact": {false, false}, "/edits": {false, false}, "/help": {false, false},
	}
	for _, c := range cmds {
		exp, ok := expected[c.Name]; if !ok { t.Errorf("unexpected command: %s", c.Name); continue }
		if c.HasArg != exp.hasArg { t.Errorf("%s: hasArg = %v, want %v", c.Name, c.HasArg, exp.hasArg) }
		if c.AllowBusy != exp.allowBusy { t.Errorf("%s: allowBusy = %v, want %v", c.Name, c.AllowBusy, exp.allowBusy) }
		if c.Usage == "" || c.Description == "" { t.Errorf("%s: empty usage or description", c.Name) }
	}
}

func TestParseSlashCommand(t *testing.T) {
	if got := ParseSlashCommand(""); got.Valid || got.Error == "" { t.Fatalf("empty = %+v", got) }
	if got := ParseSlashCommand("/notacommand"); got.Valid || !strings.Contains(got.Error, "unknown command") { t.Fatalf("unknown = %+v", got) }
	if got := ParseSlashCommand("/undo"); !got.Valid || got.N != 1 || got.HasN { t.Fatalf("undo = %+v", got) }
	if got := ParseSlashCommand("/undo 5"); !got.Valid || got.N != 5 || !got.HasN { t.Fatalf("undo 5 = %+v", got) }
	if got := ParseSlashCommand("/rewind 3"); !got.Valid || got.N != 3 || !got.HasN { t.Fatalf("rewind = %+v", got) }
}

func TestFilterSlashCommandsDeterministic(t *testing.T) {
	if got := FilterSlashCommands("/"); len(got) != 6 { t.Fatalf("expected 6 commands, got %d", len(got)) }
	if got := FilterSlashCommands("/re"); len(got) == 0 || got[0].Name != "/rewind" { t.Fatalf("rewind filter = %+v", got) }
	for i := 0; i < 5; i++ { got := FilterSlashCommands("/c"); if len(got) < 2 || got[0].Name != "/compact" || got[1].Name != "/ctx" { t.Fatalf("non-deterministic iteration %d: %+v", i, got) } }
}
