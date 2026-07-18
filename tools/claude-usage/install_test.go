package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallPreservesExistingSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"theme":"dark","env":{"A":"1"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallStatusLine(path, "/opt/claude-usage"); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["theme"] != "dark" {
		t.Errorf("install clobbered unrelated settings: %v", got)
	}
	sl, ok := got["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("no statusLine written: %v", got)
	}
	if sl["type"] != "command" || sl["command"] != "/opt/claude-usage" {
		t.Errorf("bad statusLine: %v", sl)
	}
	if sl["refreshInterval"] != float64(10) {
		t.Errorf("refreshInterval must be 10 so idle sessions keep reporting, got %v", sl["refreshInterval"])
	}
}

func TestInstallCreatesFileWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := InstallStatusLine(path, "/opt/claude-usage"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("settings.json should have been created")
	}
}

func TestInstallRefusesUnparseableSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	malformed := []byte(`{not json`)
	if err := os.WriteFile(path, malformed, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallStatusLine(path, "/opt/claude-usage"); err == nil {
		t.Fatal("expected an error for unparseable settings.json, got nil")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(malformed) {
		t.Fatalf("InstallStatusLine modified an unparseable settings.json: got %q, want unchanged %q", got, malformed)
	}
}
