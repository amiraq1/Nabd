// Package tools implements what the agent can do. Every path a tool
// touches goes through Resolve first; a tool that opens a file by any
// other route is a bug, not a shortcut.
package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrOutside  = errors.New("المسار خارج جذر المشروع")
	ErrAbsolute = errors.New("المسارات المطلقة مرفوضة")
	ErrEmpty    = errors.New("مسار فارغ")
)

// Root is a resolved project directory. Construct it once, at startup,
// and hand it to every tool. Its own path is already symlink-free, so
// containment is a comparison rather than a guess.
type Root struct{ dir string }

func NewRoot(dir string) (*Root, error) {
	if dir == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			return nil, err
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	// The root itself may sit behind a symlink -- on Android, and on any
	// mac with /tmp -> /private/tmp. If we skipped this, every child of
	// the real path would look like an escape.
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("جذر غير صالح %q: %w", dir, err)
	}
	fi, err := os.Stat(real)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("الجذر ليس مجلدًا: %s", real)
	}
	return &Root{dir: real}, nil
}

func (r *Root) Dir() string { return r.dir }

// Resolve maps a tool-supplied path to a real absolute path inside the
// root, or fails. It is the only function in nabd allowed to decide that
// a path is acceptable.
//
// The order is the whole point. Cleaning first and checking after is the
// classic hole: /root/../etc looks contained until the OS walks it. And
// checking before resolving symlinks is the subtler hole: a link named
// notes.txt pointing at /etc/shadow passes every string test there is.
//
// A path that does not exist yet cannot be EvalSymlink'd, so the deepest
// existing ancestor is resolved instead and the missing tail is appended.
// That tail contains no symlinks by definition -- it contains nothing.
func (r *Root) Resolve(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", ErrEmpty
	}
	if strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("%w: بايت صفري", ErrOutside)
	}

	// Absolute input is allowed only if it is already inside the root:
	// the model does print full paths back at us, and refusing them all
	// is theatre. Everything else is relative to the root, never to cwd.
	if filepath.IsAbs(p) {
		if !within(r.dir, filepath.Clean(p)) {
			return "", fmt.Errorf("%w: %s", ErrAbsolute, p)
		}
	} else {
		p = filepath.Join(r.dir, p)
	}

	real, err := resolveDeepest(p)
	if err != nil {
		return "", err
	}
	if !within(r.dir, real) {
		return "", fmt.Errorf("%w: %s", ErrOutside, p)
	}
	return real, nil
}

// resolveDeepest returns the fully symlink-resolved form of p, walking up
// until it finds something that exists.
func resolveDeepest(p string) (string, error) {
	p = filepath.Clean(p)
	rest := ""
	for {
		real, err := filepath.EvalSymlinks(p)
		if err == nil {
			if rest == "" {
				return real, nil
			}
			// The parent is real; the tail does not exist yet. Join is
			// safe here only because Clean already removed any "..".
			return filepath.Join(real, rest), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent, base := filepath.Dir(p), filepath.Base(p)
		if parent == p || base == "." || base == string(filepath.Separator) {
			return "", fmt.Errorf("%w: لا سلف موجود", ErrOutside)
		}
		rest = filepath.Join(base, rest)
		p = parent
	}
}

// within reports whether target is root or lives under it. Rel is used
// rather than HasPrefix, because /home/user2 has the prefix /home/user.
func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Rel is for display: journals and prompts should never carry the user's
// home directory around.
func (r *Root) Rel(abs string) string {
	if rel, err := filepath.Rel(r.dir, abs); err == nil {
		return rel
	}
	return abs
}
