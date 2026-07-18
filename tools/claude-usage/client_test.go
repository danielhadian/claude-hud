package main

import (
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestSendDeliversLine(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	sink := &fakeSink{}
	go func() { _ = Serve(sock, sink, 2*time.Second) }()
	waitForSocket(t, sock)

	if err := Send(sock, "@U|five=24\n", time.Second); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if sink.String() == "@U|five=24\n" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("sink got %q", sink.String())
}

func TestSendErrorsWhenNoHolder(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "absent.sock")
	if err := Send(sock, "@U|five=1\n", 200*time.Millisecond); err == nil {
		t.Fatal("Send to a missing holder must error so the caller can spawn one")
	}
}

func TestSendDoesNotBlockForeverOnDeadListener(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock) // listener that never accepts
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// Send a large payload (10 MB) to exceed the kernel socket buffer.
	// This forces the write to block unless SetWriteDeadline properly bounds it.
	// With a 200ms timeout and no reader accepting data, the write will block
	// and the deadline must terminate it. If SetWriteDeadline is missing,
	// this test hangs indefinitely and fails the 2s wall-clock bound.
	largePayload := make([]byte, 10*1024*1024)
	for i := range largePayload {
		largePayload[i] = 'x'
	}
	largePayload[len(largePayload)-1] = '\n'

	done := make(chan struct{})
	go func() { _ = Send(sock, string(largePayload), 200*time.Millisecond); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Send must respect its timeout - it runs inside the statusline")
	}
}
