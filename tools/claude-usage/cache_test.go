package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// errSink is a Sink whose Write can be made to fail. Named distinctly from
// holder_test.go's fakeSink, which lives in this same package.
type errSink struct {
	mu   sync.Mutex
	got  []byte
	fail error
}

func (e *errSink) Write(b []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fail != nil {
		return 0, e.fail
	}
	e.got = append(e.got, b...)
	return len(b), nil
}
func (e *errSink) Close() error { return nil }
func (e *errSink) String() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return string(e.got)
}

func TestSaveLoadCacheRoundTrips(t *testing.T) {
	dir := t.TempDir()
	if err := saveCache(dir, "@U|five=4|seven=10\n"); err != nil {
		t.Fatal(err)
	}
	got := loadCache(dir)
	if len(got) != 1 || got[0] != "@U|five=4|seven=10" {
		t.Errorf("loadCache() = %q; want one @U line", got)
	}
}

func TestSaveCacheUpsertsWithoutDisturbingTheOtherKind(t *testing.T) {
	dir := t.TempDir()
	if err := saveCache(dir, "@U|five=4\n"); err != nil {
		t.Fatal(err)
	}
	if err := saveCache(dir, "@S|sessions=2|busy=1\n"); err != nil {
		t.Fatal(err)
	}
	// A new @U must replace the old @U and leave @S intact.
	if err := saveCache(dir, "@U|five=9\n"); err != nil {
		t.Fatal(err)
	}
	got := loadCache(dir)
	if len(got) != 2 || got[0] != "@U|five=9" || got[1] != "@S|sessions=2|busy=1" {
		t.Fatalf("after @U upsert: %q; want [@U|five=9 @S|sessions=2|busy=1]", got)
	}
	// And the mirror: a new @S must replace @S and leave @U intact.
	if err := saveCache(dir, "@S|sessions=3|busy=0\n"); err != nil {
		t.Fatal(err)
	}
	got = loadCache(dir)
	if len(got) != 2 || got[0] != "@U|five=9" || got[1] != "@S|sessions=3|busy=0" {
		t.Errorf("after @S upsert: %q; want [@U|five=9 @S|sessions=3|busy=0]", got)
	}
}

func TestLoadCacheMissingReturnsEmpty(t *testing.T) {
	if got := loadCache(t.TempDir()); len(got) != 0 {
		t.Errorf("missing cache = %q; want empty", got)
	}
	if got := loadCache("/nonexistent/dir/at/all"); len(got) != 0 {
		t.Errorf("missing dir = %q; want empty", got)
	}
}

func TestLoadCacheGarbageIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(cachePath(dir), []byte("garbage\n\x00\xff\nnot a line"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadCache(dir); len(got) != 0 { // must not panic, must not invent lines
		t.Errorf("garbage cache = %q; want empty", got)
	}
}

func TestSaveCacheIgnoresUnknownPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := saveCache(dir, "hello world\n"); err != nil {
		t.Fatal(err)
	}
	if got := loadCache(dir); len(got) != 0 {
		t.Errorf("unknown prefix cached = %q; want empty", got)
	}
}

func TestCachingSinkForwardsAndCaches(t *testing.T) {
	dir := t.TempDir()
	inner := &errSink{}
	cs := &cachingSink{Sink: inner, dir: dir}

	n, err := cs.Write([]byte("@U|five=7\n"))
	if err != nil || n != len("@U|five=7\n") {
		t.Fatalf("Write() = %d,%v", n, err)
	}
	if inner.String() != "@U|five=7\n" {
		t.Errorf("inner sink got %q; want the bytes forwarded verbatim", inner.String())
	}
	if got := loadCache(dir); len(got) != 1 || got[0] != "@U|five=7" {
		t.Errorf("cache = %q; want the line recorded", got)
	}
}

func TestCachingSinkPropagatesWriteError(t *testing.T) {
	// The holder detects an unplugged board by Serve seeing a write error.
	// If cachingSink swallowed it, the holder would hang onto a dead fd and
	// the flock forever, and the panel would never recover from a replug.
	boom := errors.New("device gone")
	cs := &cachingSink{Sink: &errSink{fail: boom}, dir: t.TempDir()}
	if _, err := cs.Write([]byte("@U|five=1\n")); !errors.Is(err, boom) {
		t.Fatalf("Write() err = %v; want the underlying error propagated", err)
	}
}

func TestCachePathUnderConfigDir(t *testing.T) {
	dir := t.TempDir()
	if got, want := cachePath(dir), filepath.Join(dir, "last.json"); got != want {
		t.Errorf("cachePath() = %q; want %q", got, want)
	}
}

func TestCacheMergesPartialUsageAndAgesCountdowns(t *testing.T) {
	dir := t.TempDir()
	if err := saveCacheAt(dir, "@U|five=4|seven=10|five_s=100|seven_s=300", at(1000)); err != nil {
		t.Fatal(err)
	}
	if err := saveCacheAt(dir, "@U|five=5|five_s=80", at(1020)); err != nil {
		t.Fatal(err)
	}
	if err := saveCacheAt(dir, "@S|sessions=2|busy=1", at(1030)); err != nil {
		t.Fatal(err)
	}
	got := loadCacheAt(dir, at(1050))
	if len(got) != 2 || got[0] != "@U|five=5|five_s=50|seven=10|seven_s=250" {
		t.Fatalf("lost fields or replayed stale countdown: %q", got)
	}
	got = loadCacheAt(dir, at(1500))
	if len(got) != 2 || got[0] != "@U|five=5|five_s=0|seven=10|seven_s=0" || got[1] != "@S|sessions=0|busy=0" {
		t.Fatalf("expired cache = %q", got)
	}
}

func TestCachingSinkShortWritesKeepCompleteLine(t *testing.T) {
	dir := t.TempDir()
	inner := &fakeSink{shortWrite: true}
	cs := &cachingSink{Sink: inner, dir: dir}
	line := "@U|five=7|seven=10\n"
	if err := fullWrite(cs, []byte(line)); err != nil {
		t.Fatal(err)
	}
	if got := loadCache(dir); len(got) != 1 || got[0] != strings.TrimSpace(line) {
		t.Fatalf("cached partial write: %q", got)
	}
	if inner.String() != line {
		t.Fatalf("serial data = %q", inner.String())
	}
}
