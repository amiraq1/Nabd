//go:build unix

package safefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// mkdirat is a test seam so a test can simulate a concurrent creator winning
// the ENOENT → mkdirat race. Production always calls unix.Mkdirat.
var mkdirat = unix.Mkdirat

// openRootDirFd resolves the anchored root once and opens it as a directory
// descriptor. The root is the trust boundary the operator chose, so only its
// own links are resolved.
func openRootDirFd(rootPath string) (int, error) {
	root, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return -1, err
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY, 0)
	if err != nil {
		return -1, &os.PathError{Op: "open", Path: root, Err: err}
	}
	return fd, nil
}

// classifyDirOpenError maps a directory-component openat failure. Every
// component, including the final one, is opened with O_NOFOLLOW|O_DIRECTORY, so
// Android reports ENOTDIR for both a symlink and a non-directory; ELOOP is
// accepted defensively. Both are ErrNotDirectory: the requirement is refusal,
// not a precise type.
func classifyDirOpenError(err error, path string) error {
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return fmt.Errorf("%w: %s", ErrNotDirectory, path)
	}
	return &os.PathError{Op: "openat", Path: path, Err: err}
}

// OpenDir opens relativeDir beneath rootPath with a descriptor-relative walk.
//
// The returned *os.File owns a descriptor for the final directory and must be
// closed by the caller. "." returns the root descriptor itself. Every component
// is opened with openFlagsDir (O_NOFOLLOW|O_DIRECTORY), so a symlink or a
// non-directory component is refused as ErrNotDirectory.
//
// This implementation is unix-only; other platforms return
// ErrUnsupportedPlatform (open_dir_other.go).
func OpenDir(rootPath, relativeDir string) (*os.File, error) {
	rel, err := Normalize(relativeDir)
	if err != nil {
		return nil, err
	}

	rootFd, err := openRootDirFd(rootPath)
	if err != nil {
		return nil, err
	}
	if rel == "." {
		return os.NewFile(uintptr(rootFd), rel), nil
	}
	return walkDirFd(rootFd, rel)
}

// walkDirFd descends from an owned root descriptor. It closes every descriptor
// it owns on any failure, including the root descriptor it was given.
func walkDirFd(rootFd int, rel string) (*os.File, error) {
	comps := strings.Split(rel, string(filepath.Separator))
	prev := rootFd
	cur := ""
	for i, comp := range comps {
		cur = filepath.Join(cur, comp)
		last := i == len(comps)-1

		fd, err := unix.Openat(prev, comp, openFlagsDir, 0)
		// prev is always owned here and is never the descriptor just returned.
		unix.Close(prev)
		if err != nil {
			return nil, classifyDirOpenError(err, cur)
		}
		prev = fd
		if last {
			return os.NewFile(uintptr(fd), rel), nil
		}
	}

	// Unreachable: Normalize rejects the empty path and "." was handled above.
	return nil, ErrEmptyPath
}

// OpenOrCreateDir opens relativeDir beneath rootPath, creating missing
// directories with mkdirat as it walks. The returned *os.File owns a descriptor
// for the final directory and must be closed by the caller.
//
// New directories inherit the mode of the nearest existing ancestor (the root
// included), falling back to fallbackMode only when no usable mode is found.
// The mode is then applied with Fchmod, so a restrictive umask cannot narrow
// it. Directories created before a later failure are left in place, matching
// the existing MkdirAll behaviour.
//
// This implementation is unix-only; other platforms return
// ErrUnsupportedPlatform (open_dir_other.go).
func OpenOrCreateDir(rootPath, relativeDir string, fallbackMode os.FileMode) (*os.File, error) {
	rel, err := Normalize(relativeDir)
	if err != nil {
		return nil, err
	}

	rootFd, err := openRootDirFd(rootPath)
	if err != nil {
		return nil, err
	}
	if rel == "." {
		return os.NewFile(uintptr(rootFd), rel), nil
	}

	// inherited is the mode applied to newly created directories. It starts
	// from the root's own mode and is refreshed from each existing directory we
	// pass through.
	inherited := fallbackMode
	var rst unix.Stat_t
	if err := unix.Fstat(rootFd, &rst); err == nil {
		if m := os.FileMode(rst.Mode & 0o7777); m.Perm() != 0 {
			inherited = m
		}
	}

	comps := strings.Split(rel, string(filepath.Separator))
	prev := rootFd
	cur := ""
	for _, comp := range comps {
		cur = filepath.Join(cur, comp)

		fd, err := unix.Openat(prev, comp, openFlagsDir, 0)
		if err == nil {
			// Existing directory: adopt its mode for any later creation.
			var st unix.Stat_t
			if ferr := unix.Fstat(fd, &st); ferr == nil {
				if m := os.FileMode(st.Mode & 0o7777); m.Perm() != 0 {
					inherited = m
				}
			}
			unix.Close(prev)
			prev = fd
			continue
		}

		// Only a genuinely missing component is created. Any other failure
		// (symlink, non-directory, permissions) is refused.
		if !errors.Is(err, unix.ENOENT) {
			unix.Close(prev)
			return nil, classifyDirOpenError(err, cur)
		}

		mode := inherited
		if mode.Perm() == 0 {
			mode = fallbackMode
		}
		created := false
		if merr := mkdirat(prev, comp, uint32(mode.Perm())); merr != nil {
			// EEXIST is a lost creation race: fall through to openat, which
			// validates the winner's result. The winner's directory must not be
			// modified (see the created flag below).
			if !errors.Is(merr, unix.EEXIST) {
				unix.Close(prev)
				return nil, &os.PathError{Op: "mkdirat", Path: cur, Err: merr}
			}
		} else {
			created = true
		}

		fd, oerr := unix.Openat(prev, comp, openFlagsDir, 0)
		if oerr != nil {
			unix.Close(prev)
			return nil, classifyDirOpenError(oerr, cur)
		}
		// Only the directory this call created may be chmodded. Mkdirat's mode
		// is masked by the umask and Fchmod corrects that; a directory that won
		// an EEXIST race belongs to another writer, and changing its mode would
		// alter a file this process does not own.
		if created {
			if cerr := unix.Fchmod(fd, uint32(mode.Perm())); cerr != nil {
				unix.Close(fd)
				unix.Close(prev)
				return nil, &os.PathError{Op: "fchmod", Path: cur, Err: cerr}
			}
		}
		// Adopt the directory's mode (the winner's included) for later creation.
		var st unix.Stat_t
		if ferr := unix.Fstat(fd, &st); ferr == nil {
			if m := os.FileMode(st.Mode & 0o7777); m.Perm() != 0 {
				inherited = m
			}
		}
		unix.Close(prev)
		prev = fd
	}

	return os.NewFile(uintptr(prev), rel), nil
}
