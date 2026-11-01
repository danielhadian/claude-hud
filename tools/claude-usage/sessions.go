package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	sessionActiveWindow = 60 * time.Second // seen this recently => counted
	busyWindow          = 30 * time.Second // cost moved this recently => busy
	staleWindow         = 10 * time.Minute // older than this => pruned
)

type sessionState struct {
	LastSeen     int64   `json:"last_seen"`      // unix seconds
	LastCost     float64 `json:"last_cost"`      // total_cost_usd
	LastCostMove int64   `json:"last_cost_move"` // unix seconds cost last changed
}

// sessionFile keys state by session_id, hex-encoded so an id can never contain
// a path separator or escape the state dir.
func sessionFile(dir, id string) string {
	return filepath.Join(dir, fmt.Sprintf("%x.json", id))
}

// RecordSession writes this invocation's state. Requires refreshInterval so idle
// sessions keep reporting and do not fall out of the count.
func RecordSession(dir string, p *Payload, now time.Time) error {
	if p == nil || p.SessionID == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := sessionFile(dir, p.SessionID)

	st := sessionState{}
	hadBaseline := false
	if b, err := os.ReadFile(path); err == nil {
		var prev sessionState
		if json.Unmarshal(b, &prev) == nil {
			st = prev
			hadBaseline = true
		}
	}
	if hadBaseline && st.LastCost != p.Cost.TotalCostUSD {
		st.LastCostMove = now.Unix()
	}
	st.LastCost = p.Cost.TotalCostUSD
	st.LastSeen = now.Unix()

	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return writeAtomic(path, b)
}

// SessionLine renders @S from the state dir, pruning stale files as it goes.
func SessionLine(dir string, now time.Time) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "@S|sessions=0|busy=0\n" // no state dir yet: honestly report nothing
	}
	total, busy := 0, 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue // skip a mid-write "*.json.tmp" and anything not our state file
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var st sessionState
		if json.Unmarshal(b, &st) != nil {
			continue
		}
		age := now.Sub(time.Unix(st.LastSeen, 0))
		if age > staleWindow {
			_ = os.Remove(path)
			continue
		}
		if age > sessionActiveWindow {
			continue
		}
		total++
		if st.LastCostMove > 0 && now.Sub(time.Unix(st.LastCostMove, 0)) <= busyWindow {
			busy++
		}
	}
	return fmt.Sprintf("@S|sessions=%d|busy=%d\n", total, busy)
}
