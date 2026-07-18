//go:build windows

package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// SpawnHolder starts `exe --hold` detached from this process's console and
// process group, so it survives the statusline exiting - the Windows
// equivalent of the Unix Setsid spawn. HideWindow keeps it from flashing a
// console window on machines where the statusline runs from a GUI shell.
func SpawnHolder(exe string) error {
	cmd := exec.Command(exe, "--hold")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	// Deliberately no Wait: the holder outlives us.
	return cmd.Process.Release()
}
