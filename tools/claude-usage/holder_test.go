package main

import (
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeSink struct {
	mu               sync.Mutex
	got              []byte
	closed           bool
	wroteAfterClosed bool
	writeErr         error // when set, Write fails instead of recording bytes
	shortWrite       bool  // when set, Write accepts one byte per call (partial write)
}

func (f *fakeSink) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		f.wroteAfterClosed = true
	}
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	if f.shortWrite && len(b) > 1 { // mimic go.bug.st/serial: n < len(b), err == nil
		f.got = append(f.got, b[0])
		return 1, nil
	}
	f.got = append(f.got, b...)
	return len(b), nil
}

// SetWriteErr configures Write to fail from here on, simulating a device
// (e.g. an unplugged serial port) that is gone. Mutex-guarded like every
// other field since Serve's handler goroutine reads it concurrently.
func (f *fakeSink) SetWriteErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writeErr = err
}
func (f *fakeSink) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}
func (f *fakeSink) String() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.got)
}

// Closed is mutex-guarded: Serve closes the sink from another goroutine, so a
// bare field read here would trip the race detector.
func (f *fakeSink) Closed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// WroteAfterClosed is mutex-guarded for the same reason as Closed.
func (f *fakeSink) WroteAfterClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.wroteAfterClosed
}

// TestFullWriteLoopsOnShortWrites proves fullWrite delivers every byte even when
// the sink returns a partial count (go.bug.st/serial's Write is a single
// unix.Write with no loop). A bare Write would drop the tail -- including the
// framing '\n' -- and corrupt the firmware's next line.
func TestFullWriteLoopsOnShortWrites(t *testing.T) {
	sink := &fakeSink{shortWrite: true}
	line := []byte("@U|five=24|seven=41|five_s=3120\n")
	if err := fullWrite(sink, line); err != nil {
		t.Fatal(err)
	}
	if got := sink.String(); got != string(line) {
		t.Fatalf("fullWrite dropped bytes on short writes: got %q, want %q", got, string(line))
	}
}

// TestFullWritePropagatesError confirms a write error still surfaces (so the
// holder's unplug detection keeps working) instead of looping forever.
func TestFullWritePropagatesError(t *testing.T) {
	sink := &fakeSink{}
	sink.SetWriteErr(errors.New("device disconnected"))
	if err := fullWrite(sink, []byte("@U|five=1\n")); err == nil {
		t.Fatal("fullWrite must return the sink's write error")
	}
}

func TestServeWritesLinesThroughToSink(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	sink := &fakeSink{}
	done := make(chan error, 1)
	go func() { done <- Serve(sock, sink, 2*time.Second) }()

	waitForSocket(t, sock)
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("@U|five=24\n@S|sessions=1|busy=0\n")); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sink.String() == "@U|five=24\n@S|sessions=1|busy=0\n" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("sink got %q", sink.String())
}

func TestServeExitsWhenIdleAndClosesPort(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	sink := &fakeSink{}
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- Serve(sock, sink, 300*time.Millisecond) }()

	select {
	case <-done:
		if time.Since(start) < 250*time.Millisecond {
			t.Fatal("exited too early")
		}
		if !sink.Closed() {
			t.Fatal("idle exit must close the port so `make usage` can flash")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("holder never exited on idle")
	}
}

func TestSecondHolderFailsToBind(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	go func() { _ = Serve(sock, &fakeSink{}, 2*time.Second) }()
	waitForSocket(t, sock)
	sink := &fakeSink{}
	if err := Serve(sock, sink, time.Second); err == nil {
		t.Fatal("second holder should fail to bind")
	}
	if !sink.Closed() {
		t.Fatal("failed listener leaked the sink")
	}
}

// TestPostIdleCloseWriteDoesNotReachSink reproduces the use-after-close
// interleaving: a handler blocked in Scan() on a connection the client never
// closes must not be able to write to the sink once the idle watchdog has
// already closed it. Before the fix (single mutex, no closed flag) the
// handler's sink.Write after Serve returns races/writes straight into the
// already-closed sink; this test asserts that write is dropped instead.
func TestPostIdleCloseWriteDoesNotReachSink(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	sink := &fakeSink{}
	done := make(chan error, 1)
	go func() { done <- Serve(sock, sink, 300*time.Millisecond) }()

	waitForSocket(t, sock)
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Partial line, no trailing newline: the handler blocks in Scan().
	if _, err := conn.Write([]byte("@U|five=1")); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
		if !sink.Closed() {
			t.Fatal("idle exit must close the port")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("holder never exited on idle")
	}

	// Try completing the line after shutdown. The idle read deadline may have
	// already closed this connection; either way, no data may reach the sink.
	_, _ = conn.Write([]byte("|sub=Max 20x\n"))
	time.Sleep(200 * time.Millisecond)

	if sink.WroteAfterClosed() {
		t.Fatal("write reached the sink after Close: use-after-close on Sink")
	}
	if got := sink.String(); got != "" {
		t.Fatalf("sink recorded data after close: %q", got)
	}
}

// TestServeExitsOnSinkWriteError reproduces the unplug/replug bug: the board
// disappears, sink.Write starts failing, and the holder must stop serving
// instead of swallowing the error. Idle is set to 30s -- far longer than the
// 2s bound below -- so the only way this test can pass is the write error
// itself triggering the exit. Before the fix (`_, _ = sink.Write(line)`),
// the error is discarded, touch() still fires, the idle watchdog never sees
// a quiet period, and Serve would block until the 30s idle timeout, tripping
// the 2s deadline below and failing the test.
func TestServeExitsOnSinkWriteError(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	sink := &fakeSink{}
	sink.SetWriteErr(errors.New("write /dev/ttyUSB0: device disconnected"))
	done := make(chan error, 1)
	go func() { done <- Serve(sock, sink, 30*time.Second) }()

	waitForSocket(t, sock)
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("@U|five=1\n")); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
		if !sink.Closed() {
			t.Fatal("write-error exit must still close the sink")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("holder did not exit promptly after a sink write error; a discarded write error wedges the holder until the idle timeout, which never releases the flock for a replugged board")
	}
}

// TestReplayCacheWaitsForBoardBoot proves the holder does not fire the cached
// @U/@S lines the instant it opens the port. Opening resets the auto-reset board
// (measured 10/10, docs/HARDWARE.md); bytes sent while it is still in its boot
// ROM never reach the sketch, so an immediate replay is lost and the panel stays
// on "connecting" until the next live push. replayCache must wait out the boot,
// THEN send the real cached values, so a reconnect/reboot repaints promptly.
func TestReplayCacheWaitsForBoardBoot(t *testing.T) {
	dir := t.TempDir()
	if err := saveCache(dir, "@U|five=26|seven=31|sub=Max 20x\n"); err != nil {
		t.Fatal(err)
	}
	if err := saveCache(dir, "@S|sessions=2|busy=1\n"); err != nil {
		t.Fatal(err)
	}
	sink := &fakeSink{}
	settle := 60 * time.Millisecond

	start := time.Now()
	replayCache(sink, dir, settle)
	elapsed := time.Since(start)

	if elapsed < settle {
		t.Fatalf("replayCache sent after %v; must wait >= %v for the just-reset board to boot", elapsed, settle)
	}
	want := "@U|five=26|seven=31|sub=Max 20x\n@S|sessions=2|busy=1\n"
	if got := sink.String(); got != want {
		t.Fatalf("replayCache sent %q; want the cached lines %q", got, want)
	}
}

func waitForSocket(t *testing.T, sock string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.Dial("unix", sock); err == nil {
			c.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("socket never appeared")
}
