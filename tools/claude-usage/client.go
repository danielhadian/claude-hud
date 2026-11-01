package main

import (
	"net"
	"time"
)

// Send delivers one line to the holder. The timeout is a hard ceiling: this runs
// inside the user's statusline and must never stall Claude Code.
func Send(sock string, line string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	conn, err := net.DialTimeout("unix", sock, timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	_, err = conn.Write([]byte(line))
	return err
}
