package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func payloadWithCost(id string, cost float64) *Payload {
	pl := &Payload{SessionID: id}
	pl.Cost.TotalCostUSD = cost
	return pl
}

func TestSessionLineCountsAndBusy(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Unix(10000, 0)

	// Session A: cost moves -> busy. Session B: silent -> present, not busy.
	if err := RecordSession(dir, payloadWithCost("A", 1.00), t0); err != nil {
		t.Fatal(err)
	}
	if err := RecordSession(dir, payloadWithCost("B", 5.00), t0); err != nil {
		t.Fatal(err)
	}
	if err := RecordSession(dir, payloadWithCost("A", 1.25), t0.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := RecordSession(dir, payloadWithCost("B", 5.00), t0.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}

	got := SessionLine(dir, t0.Add(6*time.Second))
	if got != "@S|sessions=2|busy=1\n" {
		t.Errorf("SessionLine() = %q; want \"@S|sessions=2|busy=1\\n\"", got)
	}
}

func TestSessionDropsOutAfterSixtySeconds(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Unix(10000, 0)
	if err := RecordSession(dir, payloadWithCost("A", 1.0), t0); err != nil {
		t.Fatal(err)
	}
	got := SessionLine(dir, t0.Add(90*time.Second))
	if got != "@S|sessions=0|busy=0\n" {
		t.Errorf("stale session should drop out; got %q", got)
	}
}

func TestBusyDecaysAfterThirtySeconds(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Unix(10000, 0)
	if err := RecordSession(dir, payloadWithCost("A", 1.0), t0); err != nil {
		t.Fatal(err)
	}
	if err := RecordSession(dir, payloadWithCost("A", 2.0), t0.Add(1*time.Second)); err != nil {
		t.Fatal(err)
	}
	// Still within busy window.
	if got := SessionLine(dir, t0.Add(10*time.Second)); got != "@S|sessions=1|busy=1\n" {
		t.Errorf("should be busy at +10s, got %q", got)
	}
	// Cost stopped moving; 40s since last change > 30s busy window.
	if got := SessionLine(dir, t0.Add(41*time.Second)); got != "@S|sessions=1|busy=0\n" {
		t.Errorf("should be idle at +41s, got %q", got)
	}
}

// TestSessionLineIgnoresTempFiles guards the write-then-rename in RecordSession:
// a ".json.tmp" left mid-write (or by a crash) must never be scanned as a
// session, or a concurrent SessionLine in another process would double-count
// while a write is in flight.
func TestSessionLineIgnoresTempFiles(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Unix(10000, 0)

	if err := RecordSession(dir, payloadWithCost("A", 1.0), t0); err != nil {
		t.Fatal(err)
	}
	// A stray temp that is itself valid, fresh JSON: without the *.json scan
	// filter this would inflate the count to 2.
	b, _ := json.Marshal(sessionState{LastSeen: t0.Unix()})
	if err := os.WriteFile(sessionFile(dir, "A")+".tmp", b, 0o644); err != nil {
		t.Fatal(err)
	}

	if got := SessionLine(dir, t0.Add(time.Second)); got != "@S|sessions=1|busy=0\n" {
		t.Errorf("temp file must not be counted; got %q", got)
	}
}

// TestRecordSessionLeavesNoTempFile confirms the rename completes, so no ".tmp"
// litters the state dir after a normal write.
func TestRecordSessionLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	if err := RecordSession(dir, payloadWithCost("A", 1.0), time.Unix(10000, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sessionFile(dir, "A") + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("RecordSession left a temp file behind: %v", err)
	}
	if _, err := os.Stat(sessionFile(dir, "A")); err != nil {
		t.Errorf("RecordSession did not produce the final state file: %v", err)
	}
}

func TestRecordSessionIgnoresEmptyID(t *testing.T) {
	dir := t.TempDir()
	if err := RecordSession(dir, payloadWithCost("", 1.0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if got := SessionLine(dir, time.Unix(2, 0)); got != "@S|sessions=0|busy=0\n" {
		t.Errorf("empty session id must not create state, got %q", got)
	}
}

func TestCorruptFileNotMarkedBusy(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Unix(10000, 0)

	// Write a corrupt state file for session A.
	path := sessionFile(dir, "A")
	if err := os.WriteFile(path, []byte("corrupt json data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Record session A twice at UNCHANGED cost.
	// With corrupt file treated as "no baseline", cost should not move and session should not be busy.
	if err := RecordSession(dir, payloadWithCost("A", 1.0), t0); err != nil {
		t.Fatal(err)
	}
	if err := RecordSession(dir, payloadWithCost("A", 1.0), t0.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}

	got := SessionLine(dir, t0.Add(6*time.Second))
	if got != "@S|sessions=1|busy=0\n" {
		t.Errorf("corrupt file should not cause false-busy; got %q", got)
	}
}

func TestStalePruningDeletesFiles(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Unix(10000, 0)

	// Record a session.
	if err := RecordSession(dir, payloadWithCost("A", 1.0), t0); err != nil {
		t.Fatal(err)
	}

	path := sessionFile(dir, "A")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file should exist after RecordSession: %v", err)
	}

	// Call SessionLine with now > 10 minutes later.
	// The session should be pruned and file deleted.
	got := SessionLine(dir, t0.Add(11*time.Minute))
	if got != "@S|sessions=0|busy=0\n" {
		t.Errorf("stale session should be pruned; got %q", got)
	}

	// Verify the file was actually deleted.
	if _, err := os.Stat(path); err == nil {
		t.Errorf("state file should have been deleted by pruning, but still exists")
	} else if !os.IsNotExist(err) {
		t.Errorf("unexpected error checking state file: %v", err)
	}
}

func TestPartialUnmarshalDoesNotLeakFields(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Unix(100010, 0)

	// Write a state file with syntactically valid JSON but a wrong-typed field.
	// The last_cost field is a string instead of a number, which causes an unmarshal error.
	// However, the unmarshal partially populates the struct before failing:
	// last_seen: 100000 (successfully decoded)
	// last_cost: "not-a-number" (fails, leaves field at zero)
	// last_cost_move: 100000 (successfully decoded)
	// This tests that we don't leak the garbage last_cost_move value.
	path := sessionFile(dir, "B")
	if err := os.WriteFile(path, []byte(`{"last_seen": 100000, "last_cost": "not-a-number", "last_cost_move": 100000}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Record session B twice at UNCHANGED cost (1.0).
	// The first call should see a baseline unmarshal error and not leak LastCostMove.
	if err := RecordSession(dir, payloadWithCost("B", 1.0), t0); err != nil {
		t.Fatal(err)
	}
	if err := RecordSession(dir, payloadWithCost("B", 1.0), t0.Add(1*time.Second)); err != nil {
		t.Fatal(err)
	}

	// Call SessionLine at a time chosen so that if LastCostMove leaked as 100000,
	// the session would be incorrectly marked busy.
	// The leaked value 100000 would give a cost-move age of 100025 - 100000 = 25 seconds,
	// which is within the 30-second busy window.
	// With the fix, LastCostMove should be 0, so the session is not busy.
	got := SessionLine(dir, t0.Add(15*time.Second))
	if got != "@S|sessions=1|busy=0\n" {
		t.Errorf("partial unmarshal leak should not cause false-busy; got %q", got)
	}

	// Verify the persisted file does not contain the leaked value.
	// With the fix, the file should have LastCostMove of 0 (or a time near t0).
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read persisted state: %v", err)
	}
	var persistedSt sessionState
	if err := json.Unmarshal(b, &persistedSt); err != nil {
		t.Fatalf("failed to unmarshal persisted state: %v", err)
	}
	// LastCostMove should NOT be the leaked value 100000.
	if persistedSt.LastCostMove == 100000 {
		t.Errorf("persisted LastCostMove should not be the leaked value 100000, but got %d", persistedSt.LastCostMove)
	}
}
