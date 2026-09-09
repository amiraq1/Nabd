package perm

import (
	"net/url"
	"strings"
	"unicode"
)

// extractSubstitutions returns the inner text of every $(...) and backtick
// expression in cmd, recursing into nesting. These are run as commands, so the
// classifier recurses into them via Classify.
func extractSubstitutions(cmd string) []string {
	var out []string
	var sb strings.Builder
	depth := 0
	inSQ, inDQ, inBT := false, false, false
	flush := func() {
		if sb.Len() > 0 {
			out = append(out, sb.String())
			sb.Reset()
		}
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case inSQ:
			if c == '\'' {
				inSQ = false
			}
		case inDQ:
			if c == '\\' && i+1 < len(cmd) {
				i++
				continue
			}
			if c == '"' {
				inDQ = false
			}
		case inBT:
			if c == '`' {
				inBT = false
				flush()
			} else {
				sb.WriteByte(c)
			}
		case c == '\'':
			inSQ = true
		case c == '"':
			inDQ = true
		case c == '`':
			inBT = true
		case c == '$' && i+1 < len(cmd) && cmd[i+1] == '(':
			i++ // consume '('
			depth = 1
			start := i + 1
			for i+1 < len(cmd) && depth > 0 {
				i++
				switch cmd[i] {
				case '(':
					depth++
				case ')':
					depth--
				case '\'':
					for i+1 < len(cmd) && cmd[i+1] != '\'' {
						i++
					}
				case '"':
					for i+1 < len(cmd) && cmd[i+1] != '"' {
						if cmd[i+1] == '\\' {
							i++
						}
						i++
					}
				}
			}
			if depth == 0 {
				out = append(out, cmd[start:i])
			}
			continue
		default:
			continue
		}
	}
	return out
}

// statements splits a command string into top-level statements on ; && and ||
// without breaking inside quotes, backticks, or $(...).
func statements(cmd string) []string {
	var out []string
	var sb strings.Builder
	inSQ, inDQ, inBT, inSub := false, false, false, false
	depth := 0
	flush := func() {
		if s := strings.TrimSpace(sb.String()); s != "" {
			out = append(out, s)
		}
		sb.Reset()
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case inSQ:
			sb.WriteByte(c)
			if c == '\'' {
				inSQ = false
			}
			continue
		case inDQ:
			sb.WriteByte(c)
			if c == '\\' && i+1 < len(cmd) {
				i++
				sb.WriteByte(cmd[i])
			} else if c == '"' {
				inDQ = false
			}
			continue
		case inBT:
			sb.WriteByte(c)
			if c == '`' {
				inBT = false
			}
			continue
		case inSub:
			sb.WriteByte(c)
			switch c {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					inSub = false
				}
			case '\'':
				for i+1 < len(cmd) && cmd[i+1] != '\'' {
					i++
					sb.WriteByte(cmd[i])
				}
				if i+1 < len(cmd) {
					i++
					sb.WriteByte(cmd[i])
				}
			case '"':
				for i+1 < len(cmd) && cmd[i+1] != '"' {
					if cmd[i+1] == '\\' {
						i++
					}
					i++
					sb.WriteByte(cmd[i])
				}
				if i+1 < len(cmd) {
					i++
					sb.WriteByte(cmd[i])
				}
			}
			continue
		}
		switch c {
		case '\'':
			inSQ = true
		case '"':
			inDQ = true
		case '`':
			inBT = true
		case '$':
			if i+1 < len(cmd) && cmd[i+1] == '(' {
				inSub = true
				depth = 1
			}
		case ';':
			flush()
			continue
		case '&':
			if i+1 < len(cmd) && cmd[i+1] == '&' {
				flush()
				i++
				continue
			}
		case '|':
			if i+1 < len(cmd) && cmd[i+1] == '|' {
				flush()
				i++
				continue
			}
		}
		sb.WriteByte(c)
	}
	flush()
	return out
}

// splitPipeline splits a statement into stages on a top-level single | .
func splitPipeline(stmt string) []string {
	var out []string
	var sb strings.Builder
	inSQ, inDQ, inBT, inSub := false, false, false, false
	depth := 0
	flush := func() {
		if s := strings.TrimSpace(sb.String()); s != "" {
			out = append(out, s)
		}
		sb.Reset()
	}
	for i := 0; i < len(stmt); i++ {
		c := stmt[i]
		switch {
		case inSQ:
			sb.WriteByte(c)
			if c == '\'' {
				inSQ = false
			}
			continue
		case inDQ:
			sb.WriteByte(c)
			if c == '\\' && i+1 < len(stmt) {
				i++
				sb.WriteByte(stmt[i])
			} else if c == '"' {
				inDQ = false
			}
			continue
		case inBT:
			sb.WriteByte(c)
			if c == '`' {
				inBT = false
			}
			continue
		case inSub:
			sb.WriteByte(c)
			switch c {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					inSub = false
				}
			case '\'':
				for i+1 < len(stmt) && stmt[i+1] != '\'' {
					i++
					sb.WriteByte(stmt[i])
				}
				if i+1 < len(stmt) {
					i++
					sb.WriteByte(stmt[i])
				}
			case '"':
				for i+1 < len(stmt) && stmt[i+1] != '"' {
					if stmt[i+1] == '\\' {
						i++
					}
					i++
					sb.WriteByte(stmt[i])
				}
				if i+1 < len(stmt) {
					i++
					sb.WriteByte(stmt[i])
				}
			}
			continue
		}
		switch c {
		case '\'':
			inSQ = true
		case '"':
			inDQ = true
		case '`':
			inBT = true
		case '$':
			if i+1 < len(stmt) && stmt[i+1] == '(' {
				inSub = true
				depth = 1
			}
		case '|':
			flush()
			continue
		}
		sb.WriteByte(c)
	}
	flush()
	return out
}

// tokenize splits a single stage into words, honouring quotes and backslash
// escapes. The quotes are stripped from the returned words.
func tokenize(s string) []string {
	var out []string
	var sb strings.Builder
	inSQ, inDQ := false, false
	flush := func() {
		if sb.Len() > 0 {
			out = append(out, sb.String())
			sb.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inSQ:
			if c == '\'' {
				inSQ = false
			} else {
				sb.WriteByte(c)
			}
			continue
		case inDQ:
			if c == '\\' && i+1 < len(s) {
				i++
				sb.WriteByte(s[i])
			} else if c == '"' {
				inDQ = false
			} else {
				sb.WriteByte(c)
			}
			continue
		case c == '\'':
			inSQ = true
			continue
		case c == '"':
			inDQ = true
			continue
		case c == '\\':
			if i+1 < len(s) {
				i++
				sb.WriteByte(s[i])
			}
			continue
		case unicode.IsSpace(rune(c)):
			flush()
			continue
		}
		sb.WriteByte(c)
	}
	flush()
	return out
}

// firstWord returns the first whitespace-delimited token without quote handling
// (used where only the raw command name matters).
func firstWord(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// baseName strips a leading backslash (alias bypass, e.g. "\rm") and any path
// prefix (e.g. "/bin/rm") so the command name can be matched.
func baseName(raw string) string {
	s := strings.TrimLeft(raw, "\\")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// hasFlag reports whether args contain any short flag whose combined letters
// include every char in shorts, or any of the given long flags (--force).
// Pass "" for shorts to check only long flags.
func hasFlag(args []string, shorts string, longs []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if strings.HasPrefix(a, "--") {
			f := strings.TrimLeft(a, "-")
			f, _, _ = strings.Cut(f, "=")
			for _, l := range longs {
				if f == l {
					return true
				}
			}
			continue
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			if shorts == "" {
				continue
			}
			combined := a[1:]
			ok := true
			for _, want := range shorts {
				if !strings.ContainsRune(combined, want) {
					ok = false
					break
				}
			}
			if ok {
				return true
			}
		}
	}
	return false
}

// hasShort reports whether the combined short flags in args contain every char
// in need (e.g. need="fd" matches -fdx, -f -d, --force -d). Matching is
// case-insensitive so "-Rf" satisfies need="rF".
func hasShort(args []string, need string) bool {
	if need == "" {
		return false
	}
	have := ""
	for _, a := range args {
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "--") {
			continue
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			have += a[1:]
		}
	}
	have = strings.ToLower(have)
	for _, want := range need {
		if !strings.ContainsRune(have, unicode.ToLower(want)) {
			return false
		}
	}
	return true
}

// joinFlags returns the flag-bearing args joined, for use in a reason string.
func joinFlags(args []string) string {
	var f []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			f = append(f, a)
		}
	}
	return strings.Join(f, " ")
}

// truncate cuts s to at most n runes, appending "…" if it was longer.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// extractHost pulls the first http(s) URL host from a command's arguments,
// skipping option flags. Returns "" if none is found.
func extractHost(args []string) string {
	for _, a := range args {
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		if strings.Contains(a, "://") {
			if u, err := url.Parse(a); err == nil && u.Host != "" {
				return u.Host
			}
			// url.Parse needs a scheme; try to grab host:port anyway.
			if i := strings.Index(a, "://"); i >= 0 {
				rest := a[i+3:]
				if j := strings.IndexAny(rest, "/"); j >= 0 {
					rest = rest[:j]
				}
				return rest
			}
		}
	}
	return ""
}

// isAllowedHost reports whether host is in the allowed list or is a
// loopback/local address (localhost, 127.0.0.1, ::1, 10.x, 192.168.x).
func isAllowedHost(host string, allowed []string) bool {
	h := strings.ToLower(host)
	for _, a := range allowed {
		if h == strings.ToLower(a) {
			return true
		}
	}
	if h == "localhost" || strings.HasPrefix(h, "127.") || strings.HasPrefix(h, "192.168.") || strings.HasPrefix(h, "10.") || strings.HasPrefix(h, "172.") || h == "::1" || strings.HasPrefix(h, "[::1]") {
		return true
	}
	return false
}
