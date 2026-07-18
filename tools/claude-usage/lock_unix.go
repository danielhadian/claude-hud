//go:build unix

package main

import (
	"os"
	"syscall"
)

// TryLock takes an exclusive non-blocking flock. ok=false means another holder
// already owns the port - the caller must exit, not proceed. The returned
// release closes the fd (dropping the lock); the lockfile itself is left behind
// deliberately, since an flock on a stale file is harmless.
func TryLock(path string) (release func(), ok bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, false, nil // held by a live holder
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, true, nil
}
