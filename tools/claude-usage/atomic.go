package main

import (
	"os"
	"path/filepath"
)

// writeAtomic gives each writer its own temporary file. A fixed .tmp name lets
// concurrent statuslines truncate or rename one another's in-progress writes.
func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".claude-usage-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
