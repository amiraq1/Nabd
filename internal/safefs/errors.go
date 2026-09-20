package safefs

import (
	"errors"
	"fmt"
)

// PublishError reports whether an atomic write had already replaced the
// destination when an error was returned. Callers must retain recovery intent
// when Published is true because the filesystem may contain the new bytes.
type PublishError struct {
	Published bool
	Err       error
}

func (e *PublishError) Error() string {
	if e == nil || e.Err == nil {
		return "safefs: publish failed"
	}
	return fmt.Sprintf("safefs: publish failed: %v", e.Err)
}

func (e *PublishError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func WasPublished(err error) bool {
	var pe *PublishError
	return errors.As(err, &pe) && pe.Published
}

// OpenRead's error sentinels are declared here, without a build tag, so every
// platform sees the same error contract — including the fail-closed platforms
// that never open anything.
var (
	// ErrSymlink reports that the final path component is a symbolic link.
	// OpenRead never follows links, so this is a refusal rather than a
	// resolution. It is produced when the platform reports ELOOP (O_NOFOLLOW).
	ErrSymlink = errors.New("safefs: final path component is a symlink")

	// ErrNotDirectory reports that an intermediate path component is not a
	// directory. On Android, O_NOFOLLOW|O_DIRECTORY reports ENOTDIR for both a
	// symlink component and a plain non-directory component, so the two are
	// deliberately not distinguished: the security requirement is refusal, not
	// a precise type.
	ErrNotDirectory = errors.New("safefs: path component is not a directory")

	// ErrNotRegular reports that the opened target is not a regular file
	// (directory, FIFO, socket, or device).
	ErrNotRegular = errors.New("safefs: target is not a regular file")

	// ErrUnsupportedPlatform reports that descriptor-relative open is not
	// implemented on this platform. It is fail-closed: callers must never
	// substitute a path-based open for it.
	ErrUnsupportedPlatform = errors.New("safefs: descriptor-relative operations are only supported on unix")

	// ErrInvalidTarget reports a path that does not name a file: the empty
	// path, ".", or a path with a trailing separator (which names a directory).
	ErrInvalidTarget = errors.New("safefs: invalid file target")
)
