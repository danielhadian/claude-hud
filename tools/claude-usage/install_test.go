package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// statusCommand reads back the registered statusLine command, failing the test
// if settings.json is not shaped as expected.
func statusCommand(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	sl, ok := got["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("no statusLine block: %v", got)
	}
	cmd, _ := sl["command"].(string)
	return cmd
}

// A user's own status line often wraps this binary to add their shell prompt.
// Replacing it with the bare path destroys that, and the loss is silent, so
// --install must refuse and leave the file byte-for-byte alone.
func TestInstallPreservesForeignStatusLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	orig := []byte(`{"statusLine":{"type":"command","command":"bash /home/u/.claude/statusline.sh"},"theme":"dark"}`)
	if err := os.WriteFile(path, orig, 0o644); err != nil {
		t.Fatal(err)
	}

	err := InstallStatusLine(path, "/opt/claude-usage", false)
	var custom *ErrCustomStatusLine
	if !errors.As(err, &custom) {
		t.Fatalf("want ErrCustomStatusLine, got %v", err)
	}
	if custom.Command != "bash /home/u/.claude/statusline.sh" {
		t.Errorf("error should carry the command it refused to replace, got %q", custom.Command)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(orig) {
		t.Fatalf("settings.json was modified despite the refusal:\n got %s\nwant %s", got, orig)
	}
}

func TestInstallForceReplacesForeignStatusLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"statusLine":{"type":"command","command":"my-own-thing"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallStatusLine(path, "/opt/claude-usage", true); err != nil {
		t.Fatal(err)
	}
	if cmd := statusCommand(t, path); cmd != shellQuote("/opt/claude-usage") {
		t.Errorf("--force should replace the command, got %q", cmd)
	}
}

func TestInstallUpdatesOwnRegistrationWithoutForce(t *testing.T) {
	for _, existing := range []string{"/opt/claude-usage", shellQuote("/opt/claude-usage")} {
		path := filepath.Join(t.TempDir(), "settings.json")
		blob := `{"statusLine":{"type":"command","command":` + strconvQuote(existing) + `}}`
		if err := os.WriteFile(path, []byte(blob), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := InstallStatusLine(path, "/opt/claude-usage", false); err != nil {
			t.Fatal(err)
		}
		if cmd := statusCommand(t, path); cmd != shellQuote("/opt/claude-usage") {
			t.Fatalf("command = %q", cmd)
		}
	}
}

func TestInstallPreservesWrapperReferencingBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	original := []byte(`{"statusLine":{"type":"command","command":"/opt/claude-usage | tee /tmp/status"}}`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	var custom *ErrCustomStatusLine
	if err := InstallStatusLine(path, "/opt/claude-usage", false); !errors.As(err, &custom) {
		t.Fatalf("expected refusal, got %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(original) {
		t.Fatalf("wrapper changed: %s (%v)", got, err)
	}
}

func TestInstallQuotesExecutablePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executes a POSIX shell fixture")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "a b's $binary")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nprintf working"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	if err := InstallStatusLine(path, exe, false); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "-c", statusCommand(t, path)).CombinedOutput()
	if err != nil || string(out) != "working" {
		t.Fatalf("registered command failed: %s (%v)", out, err)
	}
}

func TestInstallRejectsNullSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("null"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallStatusLine(path, "/opt/claude-usage", false); err == nil {
		t.Fatal("accepted null settings")
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestInstallPreservesExistingSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"theme":"dark","env":{"A":"1"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallStatusLine(path, "/opt/claude-usage", false); err != nil {
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
	if sl["type"] != "command" || sl["command"] != shellQuote("/opt/claude-usage") {
		t.Errorf("bad statusLine: %v", sl)
	}
	if sl["refreshInterval"] != float64(10) {
		t.Errorf("refreshInterval must be 10 so idle sessions keep reporting, got %v", sl["refreshInterval"])
	}
}

func TestInstallCreatesFileWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := InstallStatusLine(path, "/opt/claude-usage", false); err != nil {
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
	if err := InstallStatusLine(path, "/opt/claude-usage", false); err == nil {
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
