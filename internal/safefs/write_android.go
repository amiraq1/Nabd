//go:build android

package safefs

import (
	crand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Seams for tests. Production always uses the real syscalls.
var (
	writeFD  = unix.Write
	fsyncFD  = unix.Fsync
	renameat = unix.Renameat
)

// tempFlags creates the temporary file: write-only, create-exclusive,
// close-on-exec, and never following a symlink. O_EXCL is what guarantees a
// fresh inode rather than an existing (possibly symlinked) path.
const tempFlags = unix.O_WRONLY | unix.O_CREAT | unix.O_EXCL | unix.O_CLOEXEC | unix.O_NOFOLLOW

// WriteFileAtomic writes data to relativeFile beneath rootPath atomically.
//
// The parent directory is obtained once through OpenOrCreateDir (a
// descriptor-relative walk), a uniquely named temporary file is created inside
// that same descriptor, fully written, chmodded, and fsynced, and only then is
// it renamed over the target with renameat. A final fsync of the parent makes
// the rename durable. The target path is never opened or renamed by name, so a
// concurrent swap cannot redirect the write.
//
// This implementation is Android-only; other platforms return
// ErrUnsupportedPlatform (write_other.go).
func WriteFileAtomic(rootPath, relativeFile string, data []byte, mode os.FileMode) error {
	// Reject targets that do not name a file before Normalize can clean them
	// away ("." and a trailing separator both survive Clean as a directory).
	if relativeFile == "" {
		return ErrEmptyPath
	}
	if relativeFile == "." {
		return ErrInvalidTarget
	}
	if strings.HasSuffix(relativeFile, string(filepath.Separator)) {
		return ErrInvalidTarget
	}

	rel, err := Normalize(relativeFile)
	if err != nil {
		return err
	}

	parent := filepath.Dir(rel)
	base := filepath.Base(rel)
	if base == "" || base == "." || base == ".." {
		return ErrInvalidTarget
	}

	// Open (creating if needed) the final parent directory once. Every later
	// step is relative to this descriptor.
	parentDir, err := OpenOrCreateDir(rootPath, parent, 0o755)
	if err != nil {
		return err
	}
	defer parentDir.Close()
	parentFd := int(parentDir.Fd())

	tmpName, tmpFd, err := createTempAt(parentFd)
	if err != nil {
		return err
	}
	// tempExists tracks whether tmpName still needs unlinking. It is cleared
	// immediately after a successful rename so the deferred cleanup can never
	// delete the published target.
	tempExists := true
	defer func() {
		if tempExists {
			_ = unix.Unlinkat(parentFd, tmpName, 0)
		}
	}()

	if err := writeAll(tmpFd, data); err != nil {
		unix.Close(tmpFd)
		return err
	}
	if err := unix.Fchmod(tmpFd, uint32(mode.Perm())); err != nil {
		unix.Close(tmpFd)
		return &os.PathError{Op: "fchmod", Path: base, Err: err}
	}
	if err := fsyncFD(tmpFd); err != nil {
		unix.Close(tmpFd)
		return &os.PathError{Op: "fsync", Path: base, Err: err}
	}
	// Close the temporary before publishing; a close error is reported and the
	// deferred unlink still removes it.
	if err := unix.Close(tmpFd); err != nil {
		return &os.PathError{Op: "close", Path: base, Err: err}
	}

	// Atomic replace: the temp inode takes the target name in one step.
	if err := renameat(parentFd, tmpName, parentFd, base); err != nil {
		return &os.PathError{Op: "renameat", Path: base, Err: err}
	}
	tempExists = false

	// The rename is visible now. If the directory fsync fails the content is
	// still published; only crash durability is lost, so this is reported as a
	// durability error and the target is never rolled back.
	if err := fsyncFD(parentFd); err != nil {
		return fmt.Errorf("safefs: publish succeeded but parent directory fsync failed: %w", err)
	}
	return nil
}

// createTempAt creates a uniquely named temporary file inside dirFd, retrying
// on EEXIST. The name is random so a concurrent writer cannot predict it.
func createTempAt(dirFd int) (string, int, error) {
	const maxAttempts = 32
	var rnd [8]byte
	for i := 0; i < maxAttempts; i++ {
		if _, err := crand.Read(rnd[:]); err != nil {
			return "", -1, err
		}
		name := ".ag-tmp-" + hex.EncodeToString(rnd[:])
		fd, err := unix.Openat(dirFd, name, tempFlags, 0o600)
		if err == nil {
			return name, fd, nil
		}
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		return "", -1, &os.PathError{Op: "openat", Path: name, Err: err}
	}
	return "", -1, fmt.Errorf("safefs: could not create a temporary file after %d attempts", maxAttempts)
}

// writeAll writes every byte of data to fd, retrying EINTR and tolerating short
// writes. A zero-length write with no error is a failure: unix.Write must never
// return (0, nil), and treating it as progress would spin forever.
func writeAll(fd int, data []byte) error {
	for len(data) > 0 {
		n, err := writeFD(fd, data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return err
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
	}
	return nil
}
