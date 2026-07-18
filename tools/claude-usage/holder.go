package main

import (
	"bufio"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Sink is where accepted lines go. The real one is the serial port; tests use a
// buffer, which is why Serve does not open the port itself.
type Sink interface {
	Write([]byte) (int, error)
	Close() error
}

// fullWrite writes all of b, looping on short writes. go.bug.st/serial's Write
// is a single unix.Write with no loop, so it may legally return n < len(b) with
// a nil error; dropping the tail (including the framing '\n') would truncate one
// line and corrupt the firmware's next update. A partial write is progress, not
// failure.
func fullWrite(s Sink, b []byte) error {
	for len(b) > 0 {
		n, err := s.Write(b)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

func runtimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return os.TempDir() // macOS has no XDG_RUNTIME_DIR
}

// SocketPath prefers XDG_RUNTIME_DIR (Linux), falls back to TMPDIR (macOS).
func SocketPath() string { return filepath.Join(runtimeDir(), "claude-usage.sock") }

// LockPath is the sidecar flock file guarding single-holder access to the port.
func LockPath() string { return filepath.Join(runtimeDir(), "claude-usage.lock") }

// Serve owns sink for its lifetime and writes every received line to it.
// Returns after `idle` elapses with no line, closing sink so the port is
// released for flashing. A second holder fails to bind and must not steal
// the port.
func Serve(sock string, sink Sink, idle time.Duration) error {
	// A stale socket file from a killed holder would block bind; remove it only
	// if nothing is listening.
	if c, err := net.Dial("unix", sock); err == nil {
		c.Close()
		return &net.OpError{Op: "listen", Net: "unix", Err: os.ErrExist}
	}
	_ = os.Remove(sock)

	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	defer ln.Close()
	defer os.Remove(sock)

	// lastMu guards only the idle-freshness timestamp, so the watchdog's
	// time.Since(last) check is never blocked behind a slow sink.Write.
	var lastMu sync.Mutex
	last := time.Now()
	touch := func() { lastMu.Lock(); last = time.Now(); lastMu.Unlock() }

	// sinkMu guards sink writes and the closed flag together, so a handler
	// either writes before Close sets the flag (and finishes before Close can
	// acquire the lock) or observes closed == true and skips the write --
	// never a write reaching an already-closed sink.
	var sinkMu sync.Mutex
	closed := false
	defer func() {
		sinkMu.Lock()
		closed = true
		sinkMu.Unlock()
		sink.Close()
	}()

	// stop closes ln exactly once, whether triggered by the idle watchdog or by
	// a handler that hit a write error (the device is gone). Both paths unblock
	// Accept -> Serve returns -> deferred sink.Close() + flock release.
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { ln.Close() }) }

	go func() {
		for {
			time.Sleep(50 * time.Millisecond)
			lastMu.Lock()
			quiet := time.Since(last)
			lastMu.Unlock()
			if quiet >= idle {
				stop() // unblocks Accept -> Serve returns -> sink.Close()
				return
			}
		}
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			return nil // listener closed by the idle watchdog or a write failure
		}
		touch()
		go func(c net.Conn) {
			defer c.Close()
			sc := bufio.NewScanner(c)
			for sc.Scan() {
				line := []byte(sc.Text() + "\n") // Text() copies; sc.Bytes() aliases the scanner buffer
				sinkMu.Lock()
				var writeErr error
				if !closed {
					writeErr = fullWrite(sink, line)
				}
				sinkMu.Unlock()
				if writeErr != nil {
					// The device is gone (e.g. unplugged): stop serving so the
					// deferred cleanup runs and a fresh holder can be spawned
					// for the replacement device. Retrying would just spin
					// against a dead fd.
					stop()
					return
				}
				touch()
			}
		}(conn)
	}
}
