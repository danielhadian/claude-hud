package main

import (
	"os"
	"path/filepath"
	"strings"
)

// cachePath is where the last-known @U and @S lines are persisted, so a
// rebooted (unplugged/replugged) board can be repopulated instantly instead
// of showing "waiting for data" until the next statusline invocation.
func cachePath(dir string) string { return filepath.Join(dir, "last.txt") }

// saveCache upserts line into the cache file by its "@U"/"@S" prefix: a new
// @U replaces the old @U without disturbing the stored @S, and vice versa.
// Lines that are neither are ignored. Best-effort: errors are returned but
// callers (the caching sink) treat this as non-fatal.
func saveCache(dir, line string) error {
	line = strings.TrimRight(line, "\n")
	var prefix string
	switch {
	case strings.HasPrefix(line, "@U"):
		prefix = "@U"
	case strings.HasPrefix(line, "@S"):
		prefix = "@S"
	default:
		return nil // neither kind; nothing to cache
	}

	existing := loadCache(dir)
	var u, s string
	for _, l := range existing {
		if strings.HasPrefix(l, "@U") {
			u = l
		} else if strings.HasPrefix(l, "@S") {
			s = l
		}
	}
	if prefix == "@U" {
		u = line
	} else {
		s = line
	}

	var out []string
	if u != "" {
		out = append(out, u)
	}
	if s != "" {
		out = append(out, s)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(cachePath(dir), []byte(strings.Join(out, "\n")+"\n"), 0o644)
}

// loadCache never fails hard: a missing or corrupt file yields an empty
// slice, same fail-open philosophy as LoadConfig. Unparseable lines (neither
// @U nor @S) are silently ignored. Returns @U first, then @S.
func loadCache(dir string) []string {
	b, err := os.ReadFile(cachePath(dir))
	if err != nil {
		return []string{}
	}
	var u, s string
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "@U") {
			u = l
		} else if strings.HasPrefix(l, "@S") {
			s = l
		}
	}
	out := []string{}
	if u != "" {
		out = append(out, u)
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

// cachingSink decorates a Sink to keep the on-disk cache fresh with the
// latest @U/@S line on every write, without altering Serve's write-error
// semantics: a failing wrapped Write must still return that error unchanged
// so the holder's unplug detection keeps working.
type cachingSink struct {
	Sink
	dir string
}

func (c *cachingSink) Write(b []byte) (int, error) {
	_ = saveCache(c.dir, string(b)) // best-effort; caching must never break the write path
	return c.Sink.Write(b)
}
