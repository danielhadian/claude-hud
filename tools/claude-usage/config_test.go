package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigMissingReturnsDefaults(t *testing.T) {
	c := LoadConfig(t.TempDir())
	if c.Tier != "" || c.Port != "auto" || c.Baud != 115200 {
		t.Errorf("defaults = %+v; want Tier:\"\" Port:\"auto\" Baud:115200", c)
	}
}

func TestLoadConfigFillsBaudWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	// A config written by an older version has no baud field: it must default,
	// not come back 0 and break serial.Open.
	if err := os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"tier":"Pro","port":"auto"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := LoadConfig(dir); c.Baud != 115200 {
		t.Errorf("absent baud should default to 115200, got %d", c.Baud)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	if err := SaveConfig(dir, Config{Tier: "Max 20x", Port: "/dev/ttyUSB0"}); err != nil {
		t.Fatal(err)
	}
	c := LoadConfig(dir)
	if c.Tier != "Max 20x" || c.Port != "/dev/ttyUSB0" {
		t.Errorf("round trip = %+v", c)
	}
}

func TestLoadConfigCorruptFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := LoadConfig(dir) // must not panic or error - fail open
	if c.Port != "auto" {
		t.Errorf("corrupt config should yield defaults, got %+v", c)
	}
}
