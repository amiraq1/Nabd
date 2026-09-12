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

// openFlagsDir is the policy for every intermediate path component: read-only,
// close-on-exec, never follow a symlink, never block, and must be a directory.
//
// O_DIRECTORY is kept here deliberately. Dropping it would let a regular file,
// FIFO, or device component be opened before Fstat rejects it, widening the
// attack surface for no benefit. On Android, O_NOFOLLOW|O_DIRECTORY reports
// ENOTDIR for a symlink component rather than ELOOP, so a symlink intermediate
// and a plain non-directory intermediate are both classified ErrNotDirectory;
// the requirement is refusal, not a precise type.
const openFlagsDir = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_DIRECTORY

// openFlagsFile is the policy for the final component: read-only,
// close-on-exec, never follow a symlink, never block. Its type is then
// confirmed with Fstat; O_NONBLOCK is what lets a FIFO reach that check
// instead of blocking the open.
const openFlagsFile = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK

// OpenRead opens relativePath beneath rootPath for reading.
//
// It never follows a symbolic link. The root's own links are resolved once
// (the root is trusted and anchored at startup), then every remaining path
// component is opened with O_NOFOLLOW relative to the previous component's
// descriptor. Each component is opened exactly once, so there is no
// resolve-then-open window for a concurrent rename or symlink swap to redirect.
//
// The returned *os.File reads from the validated descriptor, not from the path.
// Directories, FIFOs, sockets, and devices are refused via Fstat.
//
// This implementation is unix-only. Other platforms return
// ErrUnsupportedPlatform (open_other.go).
func OpenRead(rootPath, relativePath string) (*os.File, error) {
	rel, err := Normalize(relativePath)
	if err != nil {
		return nil, err
	}
	if rel == "." {
		return nil, ErrNotRegular
	}

	// The root is anchored at startup and may itself sit behind a symlink
	// (Android temp paths). Resolve only the root, once; components below it
	// are opened with O_NOFOLLOW.
	root, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return nil, err
	}
	// O_DIRECTORY proves the anchored root is a directory before anything is
	// walked beneath it.
	rootFd, err := unix.Open(root, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: root, Err: err}
	}

	comps := strings.Split(rel, string(filepath.Separator))
	prev := rootFd
	cur := ""
	for i, comp := range comps {
		cur = filepath.Join(cur, comp)
		last := i == len(comps)-1

		flags := openFlagsDir
		if last {
			flags = openFlagsFile
		}
		fd, err := unix.Openat(prev, comp, flags, 0)
		// prev is always owned here: the root descriptor on the first
		// iteration, otherwise the previously opened directory. It is never
		// the descriptor just returned, so closing it cannot close the result.
		unix.Close(prev)
		if err != nil {
			if last {
				return nil, classifyFinalOpenError(err, cur)
			}
			return nil, classifyIntermediateOpenError(err, cur)
		}
		prev = fd

		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			unix.Close(fd)
			return nil, &os.PathError{Op: "fstat", Path: cur, Err: err}
		}

		if last {
			if st.Mode&unix.S_IFMT != unix.S_IFREG {
				unix.Close(fd)
				return nil, fmt.Errorf("%w: %s", ErrNotRegular, cur)
			}
			return os.NewFile(uintptr(fd), rel), nil
		}
		// Defensive: O_DIRECTORY already enforces this, but the explicit check
		// keeps the invariant even on a platform that ignores the flag.
		if st.Mode&unix.S_IFMT != unix.S_IFDIR {
			unix.Close(fd)
			return nil, fmt.Errorf("%w: %s", ErrNotDirectory, cur)
		}
	}

	// Unreachable: Normalize rejects the empty path and "." was handled above,
	// so comps always has at least one element.
	return nil, ErrEmptyPath
}

// classifyFinalOpenError maps a final-component openat failure. ELOOP is what
// O_NOFOLLOW produces when the final component is a symlink.
func classifyFinalOpenError(err error, path string) error {
	if errors.Is(err, unix.ELOOP) {
		return fmt.Errorf("%w: %s", ErrSymlink, path)
	}
	return &os.PathError{Op: "openat", Path: path, Err: err}
}

// classifyIntermediateOpenError maps an intermediate-component openat failure.
// With O_NOFOLLOW|O_DIRECTORY, Android reports ENOTDIR for both a symlink and a
// non-directory component; ELOOP is accepted defensively. A missing component
// (ENOENT) and other OS errors stay as PathErrors so callers can still detect
// os.ErrNotExist.
func classifyIntermediateOpenError(err error, path string) error {
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return fmt.Errorf("%w: %s", ErrNotDirectory, path)
	}
	return &os.PathError{Op: "openat", Path: path, Err: err}
}
