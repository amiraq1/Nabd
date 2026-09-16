// Package ignorefile answers one question about one path: does the session's
// ignore file cover it? It exists so that the answer has exactly one
// implementation. internal/pathindex hides matching paths from the @ picker and
// internal/perm refuses to read them; if each carried its own pattern code, the
// two would eventually disagree about what a pattern means, and the disagreement
// would be invisible: the picker would hide a file the reader still hands over.
//
// Scope is deliberately narrower than git, and every gap is declared:
//   - Blank lines and comments starting with '#' are ignored.
//   - A trailing '/' makes the pattern directory-only.
//   - A leading '/' or any internal '/' anchors the pattern at the root.
//   - Wildcards (*, ?, [...]) match through path.Match.
//
// Negation ('!'), '**', subdirectory ignore files, core.excludesFile and
// .git/info/exclude are out of scope. A pattern this package cannot express is a
// pattern it does not match, which errs toward hiding less, never more.
package ignorefile

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

type pattern struct {
	text     string
	dirOnly  bool
	anchored bool
	hasGlob  bool
}

// Matcher is a parsed ignore file. The zero value matches nothing, so a session
// with no ignore file behaves exactly as it did before this package existed.
type Matcher struct{ patterns []pattern }

// Empty reports whether the matcher has no pattern to apply.
func (m Matcher) Empty() bool { return len(m.patterns) == 0 }

// Parse extracts patterns from raw ignore-file bytes.
func Parse(data []byte) Matcher {
	var patterns []pattern
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		dirOnly := strings.HasSuffix(line, "/")
		line = strings.TrimSuffix(line, "/")
		anchored := strings.HasPrefix(line, "/")
		line = strings.TrimPrefix(line, "/")
		line = strings.TrimPrefix(line, "./")
		if strings.Contains(line, "/") {
			anchored = true
		}
		if line == "" {
			continue
		}
		patterns = append(patterns, pattern{
			text:     line,
			dirOnly:  dirOnly,
			anchored: anchored,
			hasGlob:  strings.ContainsAny(line, "*?["),
		})
	}
	return Matcher{patterns: patterns}
}

// LoadDir reads .gitignore from dir. A missing file, an unreadable one, or one
// that is not a regular file — a symlink included — yields an empty matcher.
//
// Refusing a symlinked ignore file is a containment property, not politeness: a
// link at the session root would otherwise let a path outside the project decide
// what the project hides.
func LoadDir(dir string) Matcher {
	if dir == "" {
		return Matcher{}
	}
	p := filepath.Join(dir, ".gitignore")
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return Matcher{}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return Matcher{}
	}
	return Parse(data)
}

// Match reports whether rel matches, and by which pattern. rel is
// root-relative and slash-separated; name is its final element.
//
// Two rules:
//
//   - A pattern matches an entry by name or path as in git (dir-only patterns
//     need isDir).
//   - Anything under a directory that any pattern matches is excluded: if a
//     proper prefix directory of rel matches, the entry is refused by that
//     prefix's pattern. Git excludes a matched directory's contents by
//     traversal; a path judged in isolation must inherit the same verdict, or
//     a bare `node_modules` rule would hide the directory from the picker
//     while read_file handed over everything inside it.
//
// An unanchored pattern is tested against name, so a bare "build" covers a
// build directory at any depth; an anchored one against the whole rel.
func (m Matcher) Match(rel, name string, isDir bool) (string, bool) {
	simple := func(p pattern, target string) bool {
		if !p.hasGlob {
			return target == p.text
		}
		matched, _ := path.Match(p.text, target)
		return matched
	}
	for _, p := range m.patterns {
		if p.dirOnly && !isDir {
			continue
		}
		target := name
		if p.anchored {
			target = rel
		}
		if simple(p, target) {
			return p.text, true
		}
	}
	// Prefix-directory inheritance. Every proper prefix directory of rel is
	// tested against every pattern: in git, a directory that matches any ignore
	// pattern has its contents excluded by traversal, so the same must hold for
	// a path judged without a traversal — otherwise `node_modules` (a bare
	// name, no trailing slash) would hide the directory in the picker while
	// read_file handed over everything in it. A prefix is always a directory,
	// so dir-only patterns apply to it directly.
	if m.Empty() {
		return "", false
	}
	for i := 0; i < len(rel); i++ {
		if rel[i] != '/' {
			continue
		}
		prefix := rel[:i]
		prefixName := prefix
		if j := strings.LastIndex(prefix, "/"); j >= 0 {
			prefixName = prefix[j+1:]
		}
		for _, p := range m.patterns {
			target := prefixName
			if p.anchored {
				target = prefix
			}
			if simple(p, target) {
				return p.text, true
			}
		}
	}
	return "", false
}
