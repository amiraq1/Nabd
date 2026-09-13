package ui

import (
	"sort"
	"strconv"
	"strings"
)

type SlashCommand struct {
	Name string
	Usage string
	Description string
	Aliases []string
	HasArg bool
	TextArg bool
	AllowBusy bool
}

var supportedSlashCommands = []SlashCommand{
	{Name: "/goal", Usage: "/goal <objective>", Description: "run an auditable goal contract", HasArg: true, TextArg: true},
	{Name: "/undo", Usage: "/undo [n]", Description: "undo file edits recorded in the journal", HasArg: true},
	{Name: "/rewind", Usage: "/rewind [n]", Description: "rewind conversation turns and restore prompt", HasArg: true},
	{Name: "/ctx", Usage: "/ctx", Description: "show context window token usage"},
	{Name: "/compact", Usage: "/compact", Description: "compact conversation history in background"},
	{Name: "/edits", Usage: "/edits", Description: "list pending reversible file edits"},
	{Name: "/help", Usage: "/help", Description: "show supported slash commands"},
}

func AllSlashCommands() []SlashCommand {
	out := make([]SlashCommand, len(supportedSlashCommands))
	copy(out, supportedSlashCommands)
	return out
}

func LookupSlashCommand(name string) (SlashCommand, bool) {
	for _, cmd := range supportedSlashCommands {
		if cmd.Name == name { return cmd, true }
		for _, a := range cmd.Aliases { if a == name { return cmd, true } }
	}
	return SlashCommand{}, false
}

type ParsedSlashCommand struct {
	Command SlashCommand
	RawCmd string
	Arg string
	N int
	HasN bool
	Valid bool
	Error string
}

func ParseSlashCommand(line string) ParsedSlashCommand {
	trimmed := strings.TrimSpace(line)
	f := strings.Fields(trimmed)
	if len(f) == 0 { return ParsedSlashCommand{Error: "empty command"} }
	cmd, ok := LookupSlashCommand(f[0])
	if !ok { return ParsedSlashCommand{RawCmd: f[0], Error: "unknown command: " + f[0]} }
	res := ParsedSlashCommand{Command: cmd, RawCmd: f[0], N: 1, Valid: true}
	if cmd.TextArg {
		res.Arg = strings.TrimSpace(strings.TrimPrefix(trimmed, f[0]))
		if res.Arg == "" { res.Valid = false; res.Error = "usage: " + cmd.Usage }
		return res
	}
	if len(f) > 1 {
		if !cmd.HasArg || len(f) != 2 { res.Valid = false; res.Error = "usage: " + cmd.Usage; return res }
		v, err := strconv.Atoi(f[1])
		if err != nil || v <= 0 { res.Valid = false; res.Error = "usage: " + cmd.Usage; return res }
		res.N, res.HasN = v, true
	}
	return res
}

func FilterSlashCommands(query string) []SlashCommand {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" || trimmed == "/" {
		all := AllSlashCommands(); if len(all) > 8 { return all[:8] }; return all
	}
	q := strings.ToLower(trimmed)
	qNoSlash := strings.TrimPrefix(q, "/")
	type match struct { cmd SlashCommand; rank int }
	var matches []match
	for _, cmd := range supportedSlashCommands {
		name := strings.ToLower(cmd.Name); nameNoSlash := strings.TrimPrefix(name, "/"); rank := 99
		switch {
		case name == q || nameNoSlash == qNoSlash: rank = 1
		case strings.HasPrefix(name, q) || strings.HasPrefix(nameNoSlash, qNoSlash): rank = 2
		default:
			aliasMatch := false
			for _, a := range cmd.Aliases {
				al := strings.ToLower(a)
				if strings.HasPrefix(al, q) || strings.HasPrefix(strings.TrimPrefix(al, "/"), qNoSlash) { aliasMatch = true; break }
			}
			if aliasMatch { rank = 3 } else if strings.Contains(name, qNoSlash) { rank = 4 }
		}
		if rank <= 4 { matches = append(matches, match{cmd, rank}) }
	}
	sort.Slice(matches, func(i, j int) bool { if matches[i].rank != matches[j].rank { return matches[i].rank < matches[j].rank }; return matches[i].cmd.Name < matches[j].cmd.Name })
	out := make([]SlashCommand, 0, len(matches))
	for _, m := range matches { out = append(out, m.cmd); if len(out) == 8 { break } }
	return out
}
