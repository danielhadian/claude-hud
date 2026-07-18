package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Claude Code's own session registry. One file per live session, with a real
// `status` field.
//
// This is UNDOCUMENTED and may change between Claude Code versions — which is
// why every failure path here reports ok=false and lets the caller fall back to
// the cost-movement inference in sessions.go.
//
// It is used anyway because the documented statusline payload cannot answer the
// question at all. Two things only this source knows:
//
//  1. Sessions that are open but IDLE. An idle session stops invoking its
//     statusline, so cost-inference simply never hears from it and it silently
//     drops out of the count after 60s.
//  2. Whether a session is actually working right now. Cost only updates when a
//     turn COMPLETES, so inferring "busy" from cost movement is inverted: a
//     session grinding through a long task looks idle, and one that just
//     answered looks busy for 30s. Measured against this file: inference said
//     busy=0 while a session was demonstrably mid-task.
type liveSession struct {
	PID    int    `json:"pid"`
	Status string `json:"status"` // "busy" | "idle"
}

// ClaudeSessionsDir is where Claude Code writes the registry above.
func ClaudeSessionsDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "sessions")
}

// LiveSessionLine renders @S from Claude Code's session registry.
// (pidAlive is per-OS: signal 0 on unix, OpenProcess on Windows.)
//
// ok=false means "this source could not answer" — a missing directory, an
// unreadable one, or no live sessions in it — and the caller must fall back
// rather than push a confident sessions=0.
func LiveSessionLine(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}

	total, busy, seen := 0, 0, 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		seen++
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s liveSession
		if json.Unmarshal(b, &s) != nil {
			continue // corrupt or a schema we don't recognise
		}
		if !pidAlive(s.PID) {
			continue // crashed session left its file behind
		}
		total++
		if s.Status == "busy" {
			busy++
		}
	}
	if seen == 0 || total == 0 {
		return "", false // nothing usable here: let the caller fall back
	}
	return fmt.Sprintf("@S|sessions=%d|busy=%d\n", total, busy), true
}
