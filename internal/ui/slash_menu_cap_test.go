package ui

import "testing"

// TestSlashMenuListsEveryRegisteredCommand pins the menu cap to the registered
// command set. Before this, the empty query returned the first 8 of 9 commands
// and /provider — the one command that shows a user their providers and key
// status — could only be reached by typing its name. A registered command that
// the empty query cannot list is undiscoverable, so the cap must cover the
// whole set; adding a tenth command without raising maxSlashMenuEntries fails
// here instead of silently hiding the last command.
func TestSlashMenuListsEveryRegisteredCommand(t *testing.T) {
	all := AllSlashCommands()
	got := FilterSlashCommands("")

	if maxSlashMenuEntries < len(all) {
		t.Fatalf("maxSlashMenuEntries=%d is below the %d registered commands; raise the cap or name the command you are deliberately hiding", maxSlashMenuEntries, len(all))
	}
	if len(got) != len(all) {
		t.Fatalf("empty query listed %d commands, want all %d registered", len(got), len(all))
	}

	seen := map[string]bool{}
	for _, c := range got {
		seen[c.Name] = true
	}
	if !seen["/provider"] {
		t.Fatal("/provider must be discoverable from the empty query")
	}

	// "/" is the same query as "".
	if gotSlash := FilterSlashCommands("/"); len(gotSlash) != len(all) {
		t.Fatalf(`FilterSlashCommands("/") listed %d commands, want %d`, len(gotSlash), len(all))
	}
}
