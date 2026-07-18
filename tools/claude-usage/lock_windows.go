//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// TryLock takes an exclusive non-blocking file lock via LockFileEx — the
// Windows equivalent of the Unix flock. ok=false means another holder already
// owns the port - the caller must exit, not proceed. The returned release
// unlocks and closes the handle; the lockfile itself is left behind
// deliberately, since a lock on a stale file is harmless.
func TryLock(path string) (release func(), ok bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}
	h := windows.Handle(f.Fd())
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(h,
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ol); err != nil {
		f.Close()
		return nil, false, nil // held by a live holder
	}
	return func() {
		_ = windows.UnlockFileEx(h, 0, 1, 0, ol)
		_ = f.Close()
	}, true, nil
}
