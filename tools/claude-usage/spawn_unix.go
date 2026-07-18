//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// SpawnHolder starts `exe --hold` in its own session so it survives this process
// exiting - and survives Claude Code killing the statusline's process group.
// Setsid is what buys that: measured in the spike, a setsid'd child kept running
// after its spawner was confirmed SIGKILLed by process group.
func SpawnHolder(exe string) error {
	cmd := exec.Command(exe, "--hold")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	// Deliberately no Wait: let it be orphaned and reparented to init.
	return cmd.Process.Release()
}
