package ui

import (
	"strings"
	"testing"
)

func TestSlashCommandsRegistryDefinitions(t *testing.T) {
	cmds := AllSlashCommands()
	if len(cmds) != 7 {
		t.Fatalf("expected 7 commands, got %d", len(cmds))
	}
	expected := map[string]bool{"/goal": true, "/undo": true, "/rewind": true, "/ctx": true, "/compact": true, "/edits": true, "/help": true}
	for _, c := range cmds {
		if !expected[c.Name] { t.Errorf("unexpected command: %s", c.Name) }
		if c.Usage == "" || c.Description == "" { t.Errorf("%s: empty usage or description", c.Name) }
		if c.AllowBusy { t.Errorf("%s: unexpectedly allowed while busy", c.Name) }
	}
}

func TestParseSlashCommand(t *testing.T) {
	if got := ParseSlashCommand(""); got.Valid || got.Error == "" { t.Fatalf("empty = %+v", got) }
	if got := ParseSlashCommand("/notacommand"); got.Valid || !strings.Contains(got.Error, "unknown command") { t.Fatalf("unknown = %+v", got) }
	if got := ParseSlashCommand("/undo"); !got.Valid || got.N != 1 || got.HasN { t.Fatalf("undo = %+v", got) }
	if got := ParseSlashCommand("/undo 5"); !got.Valid || got.N != 5 || !got.HasN { t.Fatalf("undo 5 = %+v", got) }
	if got := ParseSlashCommand("/rewind 3"); !got.Valid || got.N != 3 || !got.HasN { t.Fatalf("rewind = %+v", got) }
	if got := ParseSlashCommand("/goal"); got.Valid || got.Error != "usage: /goal <objective>" { t.Fatalf("empty goal = %+v", got) }
	if got := ParseSlashCommand("/goal   راجع المستودع بالكامل"); !got.Valid || got.Arg != "راجع المستودع بالكامل" { t.Fatalf("goal = %+v", got) }
}

func TestFilterSlashCommandsDeterministic(t *testing.T) {
	all := FilterSlashCommands("/")
	if len(all) != 7 { t.Fatalf("expected 7 commands, got %d", len(all)) }
	if got := FilterSlashCommands("/g"); len(got) == 0 || got[0].Name != "/goal" { t.Fatalf("goal filter = %+v", got) }
	re := FilterSlashCommands("/re")
	if len(re) == 0 || re[0].Name != "/rewind" { t.Fatalf("rewind filter = %+v", re) }
	c := FilterSlashCommands("/c")
	if len(c) < 2 || c[0].Name != "/compact" || c[1].Name != "/ctx" { t.Fatalf("c filter = %+v", c) }
	for i := 0; i < 5; i++ {
		res := FilterSlashCommands("/c")
		if res[0].Name != "/compact" || res[1].Name != "/ctx" { t.Fatalf("non-deterministic iteration %d", i) }
	}
}
