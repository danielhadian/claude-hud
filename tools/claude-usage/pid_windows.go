//go:build windows

package main

import "golang.org/x/sys/windows"

// pidAlive reports whether pid names a live process. Windows has no signal 0;
// the equivalent existence check is opening the process for limited query and
// confirming it has not exited.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false // no such process (or no access - treat as gone)
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	const stillActive = 259 // STILL_ACTIVE / STATUS_PENDING
	return code == stillActive
}
