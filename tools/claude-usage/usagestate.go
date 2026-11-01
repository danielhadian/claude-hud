package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// rate_limits in the statusline payload is a PER-SESSION snapshot: it only
// refreshes when that session completes an API turn. With several sessions
// open, each pushes every ~10 s and the device is last-write-wins — so a
// session holding a stale snapshot keeps dragging the display below the
// freshest value any session knows (measured: one session reporting five=37
// while another still pushed 36).
//
// Usage within a window is monotonically non-decreasing; it only drops when
// the window resets, which the payload marks with a new resets_at. So the fix
// is a per-window maximum keyed by resets_at, persisted across invocations.

type windowState struct {
	ResetsAt int64   `json:"resets_at"`
	Pct      float64 `json:"pct"`
}

type usageState struct {
	Five  *windowState `json:"five,omitempty"`
	Seven *windowState `json:"seven,omitempty"`
}

// usageStatePath lives directly in the config dir, NOT in the sessions state
// dir — SessionLine prunes unrecognised *.json files there.
func usageStatePath(dir string) string { return filepath.Join(dir, "usage-state.json") }

// MergeUsage folds p's rate-limit windows into the persisted per-window
// maxima, preferring the newest reset epoch. A file lock serializes the
// read/merge/write across statusline processes. Lock acquisition is bounded to
// keep the statusline responsive; storage failures remain best-effort.
func MergeUsage(dir string, p *Payload) *Payload {
	if p == nil || p.RateLimits == nil {
		return p
	}

	if os.MkdirAll(dir, 0o755) != nil {
		return p
	}
	deadline := time.Now().Add(100 * time.Millisecond)
	for {
		release, ok, err := TryLock(filepath.Join(dir, "usage-state.lock"))
		if err != nil {
			return p
		}
		if ok {
			defer release()
			break
		}
		if !time.Now().Before(deadline) {
			return p
		}
		time.Sleep(time.Millisecond)
	}
	st := usageState{}
	if b, err := os.ReadFile(usageStatePath(dir)); err == nil {
		_ = json.Unmarshal(b, &st) // corrupt state = start fresh
	}

	st.Five = mergeWindow(p.RateLimits.FiveHour, st.Five)
	st.Seven = mergeWindow(p.RateLimits.SevenDay, st.Seven)

	if b, err := json.Marshal(st); err == nil {
		_ = writeAtomic(usageStatePath(dir), b)
	}
	return p
}

// mergeWindow reconciles one payload window with its stored state, raising
// win.UsedPercentage in place when the store knows a higher value for the
// same reset epoch. Returns the new state for this window.
func mergeWindow(win *Window, st *windowState) *windowState {
	if win == nil {
		return st // window absent from this payload: state stands
	}
	if win.ResetsAt == nil {
		return st // no epoch to key on: pass the value through unmerged
	}
	if st != nil && st.ResetsAt > *win.ResetsAt {
		epoch := st.ResetsAt
		win.ResetsAt, win.UsedPercentage = &epoch, st.Pct
		return st
	}
	if st != nil && st.ResetsAt == *win.ResetsAt && st.Pct > win.UsedPercentage {
		win.UsedPercentage = st.Pct // stale session: raise to the known max
	}
	return &windowState{ResetsAt: *win.ResetsAt, Pct: win.UsedPercentage}
}
