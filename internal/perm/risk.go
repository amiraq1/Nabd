// Package perm: risk.go is a conservative, purely advisory classifier over a
// shell command string. It NEVER blocks and NEVER auto-denies: the policy
// verdict (Ask/Allow/Deny) is untouched. It only changes how the permission
// prompt is rendered, so the human eye the whole design leans on gets help
// instead of doing all the work.
//
// Design tradeoff, stated plainly: this classifier is designed for FALSE
// POSITIVES, never false negatives. When in doubt it escalates. A benign
// command flagged destructive is a moment's friction; a destructive command
// flagged benign is data loss. We choose friction. The signals below are a
// floor, not a complete model of shell behaviour — command substitution,
// eval, and encoding can defeat any string scan, and we do not pretend
// otherwise. See docs/THREAT_MODEL.md.
package perm

import (
	"nabd/internal/agent"
	"strings"
)

// networkCmds are command names that open a network connection. Used by
// IsNetwork for the --offline kill switch. It is a heuristic on the command
// string, not a network namespace.
var networkCmds = map[string]bool{
	"curl": true, "wget": true, "nc": true, "ncat": true, "netcat": true,
	"ssh": true, "scp": true, "sftp": true, "rsync": true, "ping": true,
	"ping6": true, "telnet": true, "ftp": true, "smbclient": true,
}

// shellInterpreters are command names that execute arbitrary code received on
// stdin. A network command piped into one of these is treated as exfiltration
// (download-and-run) regardless of destination.
var shellInterpreters = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true,
	"python": true, "python3": true, "python2": true, "perl": true,
	"ruby": true, "node": true, "lua": true, "php": true,
}

// IsNetwork reports whether a command string contains a network invocation.
// It scans the command and any command substitutions/backticks recursively.
func IsNetwork(cmd string) bool {
	if strings.TrimSpace(cmd) == "" {
		return false
	}
	for _, sub := range extractSubstitutions(cmd) {
		if IsNetwork(sub) {
			return true
		}
	}
	for _, stmt := range statements(cmd) {
		for _, stage := range splitPipeline(stmt) {
			if name := baseName(firstWord(stage)); networkCmds[name] {
				return true
			}
		}
	}
	return false
}

// Classify returns the highest-risk advisory label found anywhere in the
// command string, recursing into command substitutions and backticks. A
// command that matches more than one signal takes the highest level seen.
// allowedHosts is the session's declared provider host list; an outbound
// connection to a host outside it is flagged exfiltrating. Pass nil to skip
// the destination check (the pipe/base64 exfil signals still apply).
func Classify(cmd string, allowedHosts []string) agent.RiskClass {
	best := agent.RiskClass{Level: agent.RiskLow}
	for _, sub := range extractSubstitutions(cmd) {
		best = best.Max(Classify(sub, allowedHosts))
	}
	for _, stmt := range statements(cmd) {
		best = best.Max(classifyStatement(stmt, allowedHosts))
	}
	return best
}

// classifyStatement handles one top-level statement: pipeline-level exfil
// patterns plus per-stage classification.
func classifyStatement(stmt string, allowedHosts []string) agent.RiskClass {
	best := agent.RiskClass{Level: agent.RiskLow}
	best = best.Max(classifyPipeline(splitPipeline(stmt), allowedHosts))
	for _, stage := range splitPipeline(stmt) {
		best = best.Max(classifyStage(stage, allowedHosts))
	}
	return best
}

// classifyPipeline detects exfiltration patterns that only appear across
// pipeline stages: a network command piped into a shell/interpreter
// (download-and-run), or base64-decoded content piped into a network command.
func classifyPipeline(stages []string, allowedHosts []string) agent.RiskClass {
	var names []string
	for _, s := range stages {
		names = append(names, baseName(firstWord(s)))
	}
	for i := 0; i < len(names)-1; i++ {
		if networkCmds[names[i]] && shellInterpreters[names[i+1]] {
			return agent.RiskClass{
				Level:  agent.RiskExfiltrating,
				Reason: "pipe to shell: " + names[i] + " | " + names[i+1],
			}
		}
		if names[i] == "base64" && networkCmds[names[i+1]] {
			return agent.RiskClass{
				Level:  agent.RiskExfiltrating,
				Reason: "base64 to network: base64 | " + names[i+1],
			}
		}
	}
	return agent.RiskClass{Level: agent.RiskLow}
}

// classifyStage classifies a single pipeline stage from its command name and
// arguments. The first word is the command (a leading backslash and any path
// prefix are stripped, so "\rm" and "/bin/rm" both read as "rm").
func classifyStage(stage string, allowedHosts []string) agent.RiskClass {
	words := tokenize(stage)
	if len(words) == 0 {
		return agent.RiskClass{Level: agent.RiskLow}
	}
	rawName := words[0]
	// Obfuscation: the command name is itself a substitution. We cannot know
	// what it expands to, so we escalate to Unknown rather than trust it.
	if strings.HasPrefix(rawName, "$(") || strings.HasPrefix(rawName, "`") {
		return agent.RiskClass{
			Level:  agent.RiskUnknown,
			Reason: "command substitution as command: " + truncate(rawName, 40),
		}
	}
	name := baseName(rawName)
	args := words[1:]

	c := classifyCommand(name, args, allowedHosts)
	if c.Level != agent.RiskLow {
		return c
	}
	// Redirection and mv are checked after the command table so their reason
	// wins only when the command itself was not already flagged.
	if r := classifyRedirection(words); r.Level != agent.RiskLow {
		return r
	}
	return agent.RiskClass{Level: agent.RiskLow}
}

// classifyCommand holds the per-command destructive/exfiltration signals.
func classifyCommand(name string, args []string, allowedHosts []string) agent.RiskClass {
	switch name {
	case "rm":
		// rm is destructive the moment it is recursive OR force: either one
		// defeats the "are you sure" prompt. Flags are collected across all
		// tokens and matched case-insensitively so "-r -f", "-Rf", "-fr" all
		// match. Per the false-positive principle we do not require both.
		if hasShort(args, "rF") || hasFlag(args, "", []string{"recursive", "force"}) {
			return agent.RiskClass{Level: agent.RiskDestructive, Reason: "recursive delete: rm " + joinFlags(args)}
		}
	case "dd":
		return agent.RiskClass{Level: agent.RiskDestructive, Reason: "raw disk write: dd"}
	case "mkfs", "mke2fs", "mkfs.ext2", "mkfs.ext3", "mkfs.ext4", "mkfs.xfs":
		return agent.RiskClass{Level: agent.RiskDestructive, Reason: "filesystem format: " + name}
	case "truncate":
		return agent.RiskClass{Level: agent.RiskDestructive, Reason: "truncate: " + name}
	case "shred":
		return agent.RiskClass{Level: agent.RiskDestructive, Reason: "secure delete: shred"}
	case "chmod":
		if hasShort(args, "R") || hasFlag(args, "", []string{"recursive"}) {
			return agent.RiskClass{Level: agent.RiskDestructive, Reason: "recursive chmod: chmod " + joinFlags(args)}
		}
	case "chown":
		if hasShort(args, "R") || hasFlag(args, "", []string{"recursive"}) {
			return agent.RiskClass{Level: agent.RiskDestructive, Reason: "recursive chown: chown " + joinFlags(args)}
		}
	case "git":
		return classifyGit(args)
	case "curl", "wget":
		if host := extractHost(args); host != "" && !isAllowedHost(host, allowedHosts) {
			return agent.RiskClass{Level: agent.RiskExfiltrating, Reason: "outbound to non-provider host: " + host}
		}
	case "mv":
		if len(args) >= 2 {
			return agent.RiskClass{Level: agent.RiskDestructive, Reason: "move may overwrite: mv"}
		}
	}
	return agent.RiskClass{Level: agent.RiskLow}
}

// classifyGit flags the destructive git subcommands called out in the spec.
func classifyGit(args []string) agent.RiskClass {
	if len(args) == 0 {
		return agent.RiskClass{Level: agent.RiskLow}
	}
	switch args[0] {
	case "push":
		if hasFlag(args[1:], "f", []string{"force"}) {
			return agent.RiskClass{Level: agent.RiskDestructive, Reason: "force push: git push --force"}
		}
	case "reset":
		if hasFlag(args[1:], "", []string{"hard"}) {
			return agent.RiskClass{Level: agent.RiskDestructive, Reason: "hard reset: git reset --hard"}
		}
	case "clean":
		rest := args[1:]
		// git clean -fdx: force + directories + ignored. The dangerous combo is
		// force (-f) together with recursing into directories (-d).
		if hasShort(rest, "fd") || (hasFlag(rest, "", []string{"force"}) && hasShort(rest, "d")) {
			return agent.RiskClass{Level: agent.RiskDestructive, Reason: "irreversible clean: git clean -fdx"}
		}
	}
	return agent.RiskClass{Level: agent.RiskLow}
}

// classifyRedirection flags overwrite redirection (">", not append ">>") onto
// a path. We cannot know from the string alone whether the path exists, so per
// the false-positive principle any overwrite redirection is flagged.
func classifyRedirection(words []string) agent.RiskClass {
	for _, w := range words {
		// Skip append redirection (>>): it does not destroy existing content.
		if strings.Contains(w, ">>") {
			continue
		}
		// Match a bare ">" or a token with ">" glued to it (e.g. "2>file",
		// ">out"), but not ">>" which was filtered above.
		if strings.Contains(w, ">") {
			return agent.RiskClass{Level: agent.RiskDestructive, Reason: "overwrite redirection: >"}
		}
	}
	return agent.RiskClass{Level: agent.RiskLow}
}
