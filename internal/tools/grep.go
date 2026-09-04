package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"nabd/internal/perm"
	"nabd/internal/provider"
)

const (
	maxGrepHits  = 80
	maxGrepBytes = 2 * 1024 * 1024 // per file
)

type grepFiles struct{ root *Root }

var _ Classified = grepFiles{}

func (grepFiles) Class() perm.Class { return perm.ReadOnly }

func (grepFiles) Name() string { return "grep" }

func (grepFiles) Spec() provider.ToolSpec {
	return spec("grep",
		"Search files with a regular expression. Result is path:line:text.",
		`{"type":"object","properties":{
			"pattern":{"type":"string","description":"regular expression (Go RE2)"},
			"path":{"type":"string","description":"directory or file, default root"},
			"glob":{"type":"string","description":"filter like **/*.go"},
			"ignore_case":{"type":"boolean"},
			"limit":{"type":"integer"}},
		 "required":["pattern"]}`)
}

func (t grepFiles) Run(ctx context.Context, raw json.RawMessage) (string, bool, error) {
	var a struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		Glob       string `json:"glob"`
		IgnoreCase bool   `json:"ignore_case"`
		Limit      int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", false, fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(a.Pattern) == "" {
		return "", false, fmt.Errorf("empty pattern")
	}
	expr := a.Pattern
	if a.IgnoreCase {
		expr = "(?i)" + expr
	}
	// RE2 has no backtracking, so a hostile pattern cannot hang the phone.
	re, err := regexp.Compile(expr)
	if err != nil {
		return "", false, fmt.Errorf("invalid regexp: %w", err)
	}

	start := a.Path
	if strings.TrimSpace(start) == "" {
		start = "."
	}
	base, err := t.root.Resolve(start)
	if err != nil {
		return "", false, err
	}
	limit := a.Limit
	if limit <= 0 || limit > maxGrepHits {
		limit = maxGrepHits
	}
	var segs []string
	if g := strings.TrimSpace(a.Glob); g != "" {
		segs = strings.Split(filepath.ToSlash(filepath.Clean(g)), "/")
	}

	var b strings.Builder
	hits, files, truncated := 0, 0, false

	scan := func(p string) error {
		rel := filepath.ToSlash(t.root.Rel(p))
		if segs != nil && !matchSegs(segs, strings.Split(rel, "/")) {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()

		head := make([]byte, 4096)
		n, _ := f.Read(head)
		if strings.IndexByte(string(head[:n]), 0) >= 0 {
			return nil // binary
		}
		if _, err := f.Seek(0, 0); err != nil {
			return nil
		}

		sc := bufio.NewScanner(&limitedReader{f: f, left: maxGrepBytes})
		sc.Buffer(make([]byte, 0, 32*1024), 1024*1024)
		line, found := 0, false
		for sc.Scan() {
			line++
			if !re.MatchString(sc.Text()) {
				continue
			}
			if hits >= limit {
				truncated = true
				return fs.SkipAll
			}
			fmt.Fprintf(&b, "%s:%d:%s\n", rel, line, clip(strings.TrimSpace(sc.Text()), 120))
			hits++
			found = true
		}
		if found {
			files++
		}
		return nil
	}

	fi, err := os.Stat(base)
	if err != nil {
		return "", false, err
	}
	if !fi.IsDir() {
		if err := scan(base); err != nil && err != fs.SkipAll {
			return "", false, err
		}
	} else {
		err = filepath.WalkDir(base, func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.IsDir() {
				if p != base && skipDir(d.Name()) {
					return fs.SkipDir
				}
				return nil
			}
			return scan(p)
		})
		if err != nil && err != fs.SkipAll {
			return "", false, err
		}
	}

	if hits == 0 {
		return fmt.Sprintf("no match · %s", a.Pattern), true, nil
	}
	fmt.Fprintf(&b, "— %d matches in %d files", hits, files)
	if truncated {
		b.WriteString(" · truncated")
	}
	b.WriteString("\n")
	return b.String(), true, nil
}

// limitedReader caps a single file without capping the walk.
type limitedReader struct {
	f    *os.File
	left int
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.left <= 0 {
		return 0, fmt.Errorf("EOF")
	}
	if len(p) > l.left {
		p = p[:l.left]
	}
	n, err := l.f.Read(p)
	l.left -= n
	return n, err
}
