package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestAtomicConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	var wg sync.WaitGroup
	for i := byte(1); i <= 20; i++ {
		wg.Add(1)
		go func(i byte) {
			defer wg.Done()
			if err := writeAtomic(path, bytes.Repeat([]byte{i}, 4096)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 4096 || !bytes.Equal(b, bytes.Repeat(b[:1], 4096)) {
		t.Fatal("concurrent writes produced a torn file")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files leaked: %v (%v)", entries, err)
	}
}
