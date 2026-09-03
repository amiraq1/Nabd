package ui

import (
	"sort"
	"strconv"
	"strings"
)

// SlashCommand defines metadata and execution constraints for a slash command.
type SlashCommand struct {
	Name        string   // e.g. "/undo"
	Usage       string   // e.g. "/undo [n]"
	Description string   // human-readable description
	Aliases     []string // alternative command triggers (none currently)
	HasArg      bool     // whether an argument is accepted
	AllowBusy   bool     // whether permitted during an active run (all false currently)
}

// supportedSlashCommands is the single source of truth for all slash commands.
var supportedSlashCommands = []SlashCommand{
	{
		Name:        "/undo",
		Usage:       "/undo [n]",
		Description: "undo file edits recorded in the journal",
		HasArg:      true,
		AllowBusy:   false,
	},
	{
		Name:        "/rewind",
		Usage:       "/rewind [n]",
		Description: "rewind conversation turns and restore prompt",
		HasArg:      true,
		AllowBusy:   false,
	},
	{
		Name:        "/ctx",
		Usage:       "/ctx",
		Description: "show context window token usage",
		HasArg:      false,
		AllowBusy:   false,
	},
	{
		Name:        "/compact",
		Usage:       "/compact",
		Description: "compact conversation history in background",
		HasArg:      false,
		AllowBusy:   false,
	},
	{
		Name:        "/edits",
		Usage:       "/edits",
		Description: "list pending reversible file edits",
		HasArg:      false,
		AllowBusy:   false,
	},
	{
		Name:        "/help",
		Usage:       "/help",
		Description: "show supported slash commands",
		HasArg:      false,
		AllowBusy:   false,
	},
}

// AllSlashCommands returns a copy of all registered slash commands.
func AllSlashCommands() []SlashCommand {
	out := make([]SlashCommand, len(supportedSlashCommands))
	copy(out, supportedSlashCommands)
	return out
}

// LookupSlashCommand resolves a command name or alias to its definition.
func LookupSlashCommand(name string) (SlashCommand, bool) {
	for _, cmd := range supportedSlashCommands {
		if cmd.Name == name {
			return cmd, true
		}
		for _, a := range cmd.Aliases {
			if a == name {
				return cmd, true
			}
		}
	}
	return SlashCommand{}, false
}

// ParsedSlashCommand holds the parsed components of a slash command input.
type ParsedSlashCommand struct {
	Command SlashCommand
	RawCmd  string
	N       int
	HasN    bool
	Valid   bool
	Error   string
}

// ParseSlashCommand parses a command line (e.g. "/undo 2").
func ParseSlashCommand(line string) ParsedSlashCommand {
	f := strings.Fields(line)
	if len(f) == 0 {
		return ParsedSlashCommand{Error: "empty command"}
	}
	cmd, ok := LookupSlashCommand(f[0])
	if !ok {
		return ParsedSlashCommand{
			RawCmd: f[0],
			Valid:  false,
			Error:  "unknown command: " + f[0],
		}
	}
	res := ParsedSlashCommand{
		Command: cmd,
		RawCmd:  f[0],
		N:       1,
		Valid:   true,
	}
	if len(f) > 1 {
		if v, err := strconv.Atoi(f[1]); err == nil && v > 0 {
			res.N = v
			res.HasN = true
		}
	}
	return res
}

// FilterSlashCommands deterministically filters and ranks commands matching query.
// Ranking:
//  1. Exact match
//  2. Prefix match
//  3. Alias prefix match
//  4. Substring match
//  5. Alphabetical tie-break
func FilterSlashCommands(query string) []SlashCommand {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" || trimmed == "/" {
		// Return all commands in their defined order (capped at 8)
		all := AllSlashCommands()
		if len(all) > 8 {
			return all[:8]
		}
		return all
	}

	q := strings.ToLower(trimmed)
	qNoSlash := strings.TrimPrefix(q, "/")

	type match struct {
		cmd  SlashCommand
		rank int
	}

	var matches []match
	for _, cmd := range supportedSlashCommands {
		name := strings.ToLower(cmd.Name)
		nameNoSlash := strings.TrimPrefix(name, "/")

		rank := 99
		switch {
		case name == q || nameNoSlash == qNoSlash:
			rank = 1
		case strings.HasPrefix(name, q) || strings.HasPrefix(nameNoSlash, qNoSlash):
			rank = 2
		default:
			aliasMatch := false
			for _, a := range cmd.Aliases {
				al := strings.ToLower(a)
				if strings.HasPrefix(al, q) || strings.HasPrefix(strings.TrimPrefix(al, "/"), qNoSlash) {
					aliasMatch = true
					break
				}
			}
			if aliasMatch {
				rank = 3
			} else if strings.Contains(name, qNoSlash) {
				rank = 4
			}
		}

		if rank <= 4 {
			matches = append(matches, match{cmd: cmd, rank: rank})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].rank != matches[j].rank {
			return matches[i].rank < matches[j].rank
		}
		return matches[i].cmd.Name < matches[j].cmd.Name
	})

	out := make([]SlashCommand, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.cmd)
		if len(out) == 8 {
			break
		}
	}
	return out
}
