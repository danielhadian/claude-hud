package main

import (
	"os"
	"sync"
	"testing"
)

func payloadWin(fivePct float64, fiveReset int64) *Payload {
	return p(win(fivePct, fiveReset), nil)
}

// TestMergeRaisesStaleRegression reproduces the measured bug: one session
// reported five=37 while another, holding an older snapshot, pushed 36 —
// last-write-wins dragged the device back down. Same reset epoch means same
// window, and usage within a window never decreases, so the merge must raise
// the stale 36 to the known 37.
func TestMergeRaisesStaleRegression(t *testing.T) {
	dir := t.TempDir()
	MergeUsage(dir, payloadWin(37, 1000))

	got := MergeUsage(dir, payloadWin(36, 1000))
	if pct := got.RateLimits.FiveHour.UsedPercentage; pct != 37 {
		t.Errorf("stale 36 in epoch 1000 should be raised to 37, got %v", pct)
	}
}

func TestMergeAcceptsHigherAndPersists(t *testing.T) {
	dir := t.TempDir()
	MergeUsage(dir, payloadWin(36, 1000))
	MergeUsage(dir, payloadWin(38, 1000))

	// A later stale push must now be raised to the NEW maximum.
	got := MergeUsage(dir, payloadWin(36, 1000))
	if pct := got.RateLimits.FiveHour.UsedPercentage; pct != 38 {
		t.Errorf("maximum should have advanced to 38, got %v", pct)
	}
}

// TestMergeAcceptsDropOnNewWindow: a changed resets_at means the window
// genuinely reset — the drop is real and must NOT be clamped to the old max.
func TestMergeAcceptsDropOnNewWindow(t *testing.T) {
	dir := t.TempDir()
	MergeUsage(dir, payloadWin(90, 1000))

	got := MergeUsage(dir, payloadWin(3, 2000))
	if pct := got.RateLimits.FiveHour.UsedPercentage; pct != 3 {
		t.Errorf("new epoch must accept the reset value 3, got %v", pct)
	}
	// And the new epoch is the baseline now: 2 in the same new epoch stays 3.
	got = MergeUsage(dir, payloadWin(2, 2000))
	if pct := got.RateLimits.FiveHour.UsedPercentage; pct != 3 {
		t.Errorf("baseline should be 3 in the new epoch, got %v", pct)
	}
}

func TestMergeWindowsAreIndependent(t *testing.T) {
	dir := t.TempDir()
	MergeUsage(dir, p(win(37, 1000), win(23, 5000)))

	// five regresses (raise it) while seven advances (accept it).
	got := MergeUsage(dir, p(win(36, 1000), win(24, 5000)))
	if f := got.RateLimits.FiveHour.UsedPercentage; f != 37 {
		t.Errorf("five should be raised to 37, got %v", f)
	}
	if s := got.RateLimits.SevenDay.UsedPercentage; s != 24 {
		t.Errorf("seven should advance to 24, got %v", s)
	}
}

// TestMergeAbsentWindowLeavesStateStanding: omission means "no data", not
// "reset" — the stored max must survive for the next push that has the window.
func TestMergeAbsentWindowLeavesStateStanding(t *testing.T) {
	dir := t.TempDir()
	MergeUsage(dir, payloadWin(37, 1000))
	MergeUsage(dir, p(nil, win(10, 5000))) // five absent this push

	got := MergeUsage(dir, payloadWin(36, 1000))
	if pct := got.RateLimits.FiveHour.UsedPercentage; pct != 37 {
		t.Errorf("state should survive an absent-window push, got %v", pct)
	}
}

func TestMergeToleratesNilsAndCorruptState(t *testing.T) {
	dir := t.TempDir()
	if got := MergeUsage(dir, nil); got != nil {
		t.Error("nil payload must pass through")
	}
	if got := MergeUsage(dir, &Payload{}); got.RateLimits != nil {
		t.Error("payload without rate_limits must pass through")
	}
	// Window with no resets_at: no epoch to key on — value passes unmerged.
	pl := p(&Window{UsedPercentage: 12}, nil)
	if got := MergeUsage(dir, pl); got.RateLimits.FiveHour.UsedPercentage != 12 {
		t.Error("resets_at-less window must pass through unchanged")
	}
	// Corrupt state file: start fresh, never fail.
	if err := os.WriteFile(usageStatePath(dir), []byte("{corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := MergeUsage(dir, payloadWin(5, 1000)); got.RateLimits.FiveHour.UsedPercentage != 5 {
		t.Error("corrupt state must be discarded, not fatal")
	}
}

func TestMergeRejectsPreviousResetWindow(t *testing.T) {
	dir := t.TempDir()
	MergeUsage(dir, payloadWin(90, 1000))
	MergeUsage(dir, payloadWin(3, 2000))
	got := MergeUsage(dir, payloadWin(95, 1000))
	w := got.RateLimits.FiveHour
	if w.UsedPercentage != 3 || *w.ResetsAt != 2000 {
		t.Fatalf("old snapshot replaced new window: %+v", w)
	}
	got = MergeUsage(dir, payloadWin(2, 2000))
	if got.RateLimits.FiveHour.UsedPercentage != 3 {
		t.Fatal("new baseline was lost")
	}
}

func TestMergeConcurrentWindowsKeepBothMaxima(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := 1; i <= 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				MergeUsage(dir, p(win(float64(i), 1000), nil))
			} else {
				MergeUsage(dir, p(nil, win(float64(i), 2000)))
			}
		}(i)
	}
	wg.Wait()
	got := MergeUsage(dir, p(win(0, 1000), win(0, 2000)))
	if got.RateLimits.FiveHour.UsedPercentage != 20 || got.RateLimits.SevenDay.UsedPercentage != 19 {
		t.Fatalf("lost concurrent update: %+v", got.RateLimits)
	}
}
