// Package config reads nabd's settings from a file that lives outside any
// project root, so a provider key never has to travel through the shell
// environment where `env`, `cat ~/.bashrc`, or a curious child process can
// see it.
//
// The file is ~/.ag/config (override with NABD_CONFIG). Format is the
// smallest thing that works:
//
//	# comment
//	ANTHROPIC_API_KEY=sk-ant-...
//	export NVIDIA_API_KEY="nvapi-..."   # `export` and quotes are tolerated
//	NABD_MODEL=claude-sonnet-4-5
//
// Rules that matter:
//   - This package never writes. Not the file, not a temp copy, nothing.
//   - A file readable by group or others is refused, the way ssh refuses a
//     loose private key. A key you can't protect is a key you don't have.
//   - The file wins over the environment. If both are set, the file is the
//     source of truth; the environment is a fallback for people who have
//     not migrated yet.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// EnvVar names the override for the config path.
const EnvVar = "NABD_CONFIG"

var (
	once    sync.Once
	values  map[string]string
	loadErr error
)

// Path returns where the config file is expected. It does not check that
// the file exists.
func Path() (string, error) {
	if p := strings.TrimSpace(os.Getenv(EnvVar)); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ag", "config"), nil
}

// Load parses the config file once. A missing file is not an error: the
// environment still works. A present but unreadable or too-open file is.
func Load() error {
	once.Do(func() {
		values, loadErr = load()
	})
	return loadErr
}

// Get returns the value for key: file first, then environment, trimmed.
// An unloaded or broken config silently degrades to the environment; the
// caller that cares about the error should call Load first.
func Get(key string) string {
	_ = Load()
	if v, ok := values[key]; ok && v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv(key))
}

// GetOr is Get with a fallback for the empty case.
func GetOr(key, fallback string) string {
	if v := Get(key); v != "" {
		return v
	}
	return fallback
}

// Has reports whether key is set in either the file or the environment.
func Has(key string) bool { return Get(key) != "" }

// ResetForTest clears the load-once and cached values so a test can point
// NABD_CONFIG at a fresh file and observe the new load. Production code never
// calls this; the package-global Once is correct for a process lifetime.
func ResetForTest() {
	once = sync.Once{}
	values = nil
	loadErr = nil
}

func load() (map[string]string, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	return ParseFile(p)
}

// ParseFile reads and parses the file at p with the permission check.
// It is exported for tests and for anyone who wants a different location.
//
// Security: the path is checked with os.Lstat so an attacker cannot swap a
// symlink in front of the open (TOCTOU via a symlink to a loose file). A
// symlink is refused outright; only a regular file is accepted; group/other
// permission bits are refused; and on Unix the file must be owned by the
// current user (documented as a Windows limitation: NT ACL semantics make a
// numeric-uid ownership check meaningless there, so it is skipped).
func ParseFile(p string) (map[string]string, error) {
	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s: رابط مرن غير مسموح — الملف العادي مطلوب (chmod 600 على الملف الحقيقي)", p)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: غير منتظم — الملف العادي مطلوب", p)
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		return nil, fmt.Errorf("%s: الصلاحيات %04o مفتوحة للغير — شغّل: chmod 600 %s", p, mode, p)
	}
	if err := checkOwner(p, fi); err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// checkOwner enforces Unix ownership of the config file: it must belong to the
// current user, the way ssh refuses a loose private key owned by someone else.
// On non-Unix platforms it is a no-op (see owner_unix.go / owner_other.go).
func checkOwner(p string, fi os.FileInfo) error {
	return checkOwnerPlatform(p, fi)
}

// Parse reads KEY=VALUE lines. Blank lines and `#` comments are skipped;
// an optional `export ` prefix and matching single or double quotes around
// the value are stripped. A trailing ` # comment` is removed only when the
// value is unquoted, because `#` is legal inside a key.
func Parse(r interface{ Read([]byte) (int, error) }) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("سطر %d: ليس بصيغة KEY=VALUE", n)
		}
		k = strings.TrimSpace(k)
		if k == "" || strings.ContainsAny(k, " \t") {
			return nil, fmt.Errorf("سطر %d: اسم مفتاح غير صالح", n)
		}
		v = strings.TrimSpace(v)
		switch {
		case len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0]:
			v = v[1 : len(v)-1]
		default:
			if i := strings.Index(v, " #"); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
		}
		out[k] = v
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
