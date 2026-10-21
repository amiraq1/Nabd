package registry

import (
	"os"
	"path/filepath"
)

// AuthLock is an exclusive inter-process lock guarding the auth file's
// read-modify-write cycle (see providercmd.Connect). Without it, two
// concurrent writers each read the old file, add their own entry, and the
// second rename silently drops the first writer's credentials.
type AuthLock struct {
	f *os.File
}

// LockAuthFile takes an exclusive lock on a sidecar file next to path and
// holds it until Unlock. It blocks until the lock is acquired. The parent
// directory is created (0o700) so the lock works on a fresh config dir.
func LockAuthFile(path string) (*AuthLock, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockExclusive(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &AuthLock{f: f}, nil
}

// Unlock releases the lock. The sidecar file is left in place: removing it
// would race another process opening it between unlink and open.
func (l *AuthLock) Unlock() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := unlockExclusive(l.f)
	cerr := l.f.Close()
	l.f = nil
	if err != nil {
		return err
	}
	return cerr
}
