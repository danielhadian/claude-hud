package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Keep receive times per message type: a session update must not restart the
// usage countdown. Old last.txt caches are ignored and refreshed by live data.
type cachedState struct {
	Usage      string `json:"usage"`
	UsageAt    int64  `json:"usage_at"`
	Sessions   string `json:"sessions"`
	SessionsAt int64  `json:"sessions_at"`
}

func cachePath(dir string) string { return filepath.Join(dir, "last.json") }

func readCache(dir string) cachedState {
	var st cachedState
	b, err := os.ReadFile(cachePath(dir))
	if err == nil && json.Unmarshal(b, &st) != nil {
		return cachedState{}
	}
	return st
}

func saveCache(dir, line string) error { return saveCacheAt(dir, line, time.Now()) }

func saveCacheAt(dir, line string, now time.Time) error {
	line = strings.TrimRight(line, "\r\n")
	prefix, _, _ := strings.Cut(line, "|")
	if prefix != "@U" && prefix != "@S" {
		return nil
	}
	st := readCache(dir)
	if prefix == "@U" {
		// Merge fields, rather than replace the whole line: omission means unchanged.
		fields := map[string]string{}
		for _, l := range []string{ageUsage(st.Usage, now.Unix()-st.UsageAt), line} {
			parts := strings.Split(l, "|")
			for _, field := range parts[1:] {
				key, value, ok := strings.Cut(field, "=")
				if ok {
					fields[key] = value
				}
			}
		}
		keys := make([]string, 0, len(fields))
		for key := range fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := []string{"@U"}
		for _, key := range keys {
			parts = append(parts, key+"="+fields[key])
		}
		st.Usage, st.UsageAt = strings.Join(parts, "|"), now.Unix()
	} else {
		st.Sessions, st.SessionsAt = line, now.Unix()
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeAtomic(cachePath(dir), b)
}

// ageUsage preserves percentages but subtracts offline time from reset timers.
func ageUsage(line string, elapsed int64) string {
	if elapsed < 0 {
		elapsed = 0
	}
	parts := strings.Split(line, "|")
	for i, field := range parts {
		key, value, _ := strings.Cut(field, "=")
		if key != "five_s" && key != "seven_s" {
			continue
		}
		sec, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			sec = 0
		}
		if sec <= elapsed {
			sec = 0
		} else {
			sec -= elapsed
		}
		parts[i] = key + "=" + strconv.FormatInt(sec, 10)
	}
	return strings.Join(parts, "|")
}

func loadCache(dir string) []string { return loadCacheAt(dir, time.Now()) }

func loadCacheAt(dir string, now time.Time) []string {
	st := readCache(dir)
	out := []string{}
	if strings.HasPrefix(st.Usage, "@U|") {
		out = append(out, ageUsage(st.Usage, now.Unix()-st.UsageAt))
	}
	if strings.HasPrefix(st.Sessions, "@S|") {
		if now.Unix()-st.SessionsAt >= 120 {
			st.Sessions = "@S|sessions=0|busy=0"
		}
		out = append(out, st.Sessions)
	}
	return out
}

// cachingSink writes a complete line before caching it. Letting fullWrite retry
// this decorator would otherwise cache the truncated tail of a short write.
type cachingSink struct {
	Sink
	dir string
}

func (c *cachingSink) Write(b []byte) (int, error) {
	if err := fullWrite(c.Sink, b); err != nil {
		return 0, err
	}
	_ = saveCache(c.dir, string(b))
	return len(b), nil
}
