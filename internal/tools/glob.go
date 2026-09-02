package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"nabd/internal/provider"
)

const maxGlobResults = 200

type globFiles struct{ root *Root }

func (globFiles) Name() string { return "glob" }

func (globFiles) Spec() provider.ToolSpec {
	return spec("glob",
		"Find files by pattern like **/*.go or internal/*/*_test.go. Newest first. Long result lists are paged: continue with offset.",
		`{"type":"object","properties":{
			"pattern":{"type":"string"},
			"limit":{"type":"integer"},
			"offset":{"type":"integer","description":"1-based result offset for paging through a long list"}},
		 "required":["pattern"]}`)
}

func (t globFiles) Run(ctx context.Context, raw json.RawMessage) (string, bool, error) {
	var a struct {
		Pattern string `json:"pattern"`
		Limit   int    `json:"limit"`
		Offset  int    `json:"offset"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", false, fmt.Errorf("invalid args: %w", err)
	}
	pat := strings.TrimSpace(a.Pattern)
	if pat == "" {
		return "", false, errors.New("empty pattern")
	}
	if filepath.IsAbs(pat) {
		return "", false, ErrAbsolute
	}
	limit := a.Limit
	if limit <= 0 || limit > maxGlobResults {
		limit = maxGlobResults
	}
	start := a.Offset
	if start < 1 {
		start = 1
	}

	segs := strings.Split(filepath.ToSlash(filepath.Clean(pat)), "/")
	type hit struct {
		rel string
		mod time.Time
	}
	var hits []hit

	err := filepath.WalkDir(t.root.Dir(), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable corner is not a reason to fail
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if p != t.root.Dir() && skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(t.root.Dir(), p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !matchSegs(segs, strings.Split(rel, "/")) {
			return nil
		}
		fi, ferr := d.Info()
		if ferr != nil {
			return nil
		}
		hits = append(hits, hit{rel, fi.ModTime()})
		return nil
	})
	if err != nil {
		return "", false, err
	}

	// Newest first: when a model asks for *.go it wants what changed.
	sort.Slice(hits, func(i, j int) bool { return hits[i].mod.After(hits[j].mod) })

	if len(hits) == 0 {
		return fmt.Sprintf("no results · %s", pat), true, nil
	}
	total := len(hits)
	if start > total {
		return fmt.Sprintf("no results at offset=%d · %d total (newest first) · %s", start, total, pat), true, nil
	}
	end := start - 1 + limit
	if end > total {
		end = total
	}
	var b strings.Builder
	for _, h := range hits[start-1 : end] {
		b.WriteString(h.rel + "\n")
	}
	if end < total {
		// Same continuation contract as read_file: the tail names the
		// exact range shown and the offset that resumes after it — a
		// silent cut invites the model to summarise half the truth.
		fmt.Fprintf(&b, "showing %d-%d of %d · continue with offset=%d\n", start, end, total, end+1)
	}
	return b.String(), true, nil
}

// matchSegs matches a slash-split pattern against a slash-split path,
// where ** spans any number of segments and * stops at a separator.
// Written by hand rather than pulled in: it is twelve lines, and a
// dependency for twelve lines is a dependency you maintain for free.
func matchSegs(pat, name []string) bool {
	if len(pat) == 0 {
		return len(name) == 0
	}
	if pat[0] == "**" {
		if matchSegs(pat[1:], name) {
			return true
		}
		if len(name) > 0 {
			return matchSegs(pat, name[1:])
		}
		return false
	}
	if len(name) == 0 {
		return false
	}
	ok, err := filepath.Match(pat[0], name[0])
	if err != nil || !ok {
		return false
	}
	return matchSegs(pat[1:], name[1:])
}
