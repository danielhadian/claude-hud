package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeLive writes one of Claude Code's own session registry files.
func writeLive(t *testing.T, dir, name string, pid int, status string) {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"pid": pid, "sessionId": name, "status": status, "kind": "interactive",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLiveSessionLineCountsIdleSessionsToo(t *testing.T) {
	// The whole point: an OPEN BUT IDLE session must still be counted. The
	// cost-movement inference cannot see these at all, because an idle session
	// stops invoking the statusline.
	dir := t.TempDir()
	me := os.Getpid()
	writeLive(t, dir, "a", me, "busy")
	writeLive(t, dir, "b", me, "idle")

	got, ok := LiveSessionLine(dir)
	if !ok || got != "@S|sessions=2|busy=1\n" {
		t.Errorf("LiveSessionLine() = %q,%v; want \"@S|sessions=2|busy=1\\n\",true", got, ok)
	}
}

func TestLiveSessionLineIgnoresDeadPIDs(t *testing.T) {
	// A crashed session leaves its file behind; counting it would inflate the
	// dot row forever.
	dir := t.TempDir()
	writeLive(t, dir, "alive", os.Getpid(), "busy")
	writeLive(t, dir, "dead", 0x7FFFFFF0, "busy") // PID that cannot exist

	got, ok := LiveSessionLine(dir)
	if !ok || got != "@S|sessions=1|busy=1\n" {
		t.Errorf("LiveSessionLine() = %q,%v; want \"@S|sessions=1|busy=1\\n\",true", got, ok)
	}
}

func TestLiveSessionLineAbsentDirFallsBack(t *testing.T) {
	// Undocumented source: if Claude Code ever stops writing it (or on a
	// platform that doesn't), report ok=false so the caller falls back to the
	// cost inference rather than showing a confident zero.
	if _, ok := LiveSessionLine(filepath.Join(t.TempDir(), "nope")); ok {
		t.Error("absent dir must return ok=false so the caller can fall back")
	}
	if _, ok := LiveSessionLine(t.TempDir()); ok {
		t.Error("empty dir must return ok=false, not a confident sessions=0")
	}
}

func TestLiveSessionLineIgnoresCorruptFiles(t *testing.T) {
	dir := t.TempDir()
	writeLive(t, dir, "good", os.Getpid(), "busy")
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := LiveSessionLine(dir)
	if !ok || got != "@S|sessions=1|busy=1\n" {
		t.Errorf("corrupt file should be skipped; got %q,%v", got, ok)
	}
}

func TestLiveSessionLineCountsOnlyBusyAsBusy(t *testing.T) {
	dir := t.TempDir()
	me := os.Getpid()
	writeLive(t, dir, "a", me, "idle")
	writeLive(t, dir, "b", me, "idle")
	writeLive(t, dir, "c", me, "busy")
	got, ok := LiveSessionLine(dir)
	if !ok || got != "@S|sessions=3|busy=1\n" {
		t.Errorf("LiveSessionLine() = %q,%v; want 3 sessions, 1 busy", got, ok)
	}
}

func TestLiveSessionLineUnknownSchemaFallsBack(t *testing.T) {
	dir := t.TempDir()
	writeLive(t, dir, "unknown", os.Getpid(), "")
	if line, ok := LiveSessionLine(dir); ok {
		t.Fatalf("unknown status reported as idle: %q", line)
	}
}

func TestClaudeConfigDirOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if got := ClaudeConfigDir(); got != dir {
		t.Fatalf("config dir = %q", got)
	}
	if got := ClaudeSessionsDir(); got != filepath.Join(dir, "sessions") {
		t.Fatalf("sessions dir = %q", got)
	}
}
