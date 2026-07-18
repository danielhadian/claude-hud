package main

import (
	"net"
	"time"
)

// Send delivers one line to the holder. The timeout is a hard ceiling: this runs
// inside the user's statusline and must never stall Claude Code.
func Send(sock string, line string, timeout time.Duration) error {
	conn, err := net.DialTimeout("unix", sock, timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	_, err = conn.Write([]byte(line))
	return err
}
