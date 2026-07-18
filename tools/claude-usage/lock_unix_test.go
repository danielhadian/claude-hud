//go:build unix

package main

import (
	"path/filepath"
	"testing"
)

func TestTryLockExcludesSecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	release, ok, err := TryLock(path)
	if err != nil || !ok {
		t.Fatalf("first TryLock = %v,%v; want ok", ok, err)
	}
	if _, ok2, _ := TryLock(path); ok2 {
		t.Fatal("second TryLock succeeded - two holders would both drive the board")
	}
	release()
	release2, ok3, err := TryLock(path)
	if err != nil || !ok3 {
		t.Fatalf("TryLock after release = %v,%v; want ok", ok3, err)
	}
	release2()
}
