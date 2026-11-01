//go:build unix

package main

import (
	"errors"
	"os"
	"syscall"
)

// pidAlive reports whether pid names a live process. Signal 0 performs the
// permission/existence check without delivering anything.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid) // never fails on unix
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission)
}
