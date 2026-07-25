package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ErrCustomStatusLine reports that settings.json already carries a statusLine
// command this tool did not write — typically the user's own wrapper script,
// which commonly pipes the payload through claude-usage while adding its own
// text (a shell prompt, git branch, ...). Blindly replacing it with the bare
// binary path silently destroys that work, and the loss is invisible until the
// user notices their status line got shorter. --install therefore refuses, and
// --force is the deliberate override.
type ErrCustomStatusLine struct{ Command string }

func (e *ErrCustomStatusLine) Error() string {
	return "settings.json already has a statusLine command that does not reference this binary: " + e.Command
}

// existingStatusCommand returns the currently registered statusLine command, or
// "" when there is none. Anything not shaped like our block (a non-object, or a
// non-string command) reads as absent: we only want to protect a command we can
// actually show the user.
func existingStatusCommand(settings map[string]any) string {
	block, ok := settings["statusLine"].(map[string]any)
	if !ok {
		return ""
	}
	cmd, _ := block["command"].(string)
	return cmd
}

// InstallStatusLine merges a statusLine block into settings.json, preserving
// every other key. refreshInterval=10 keeps idle sessions reporting so they do
// not fall out of the session count (see sessions.go).
//
// An existing command that already references exe is ours (either the bare path
// or a wrapper that names it) and is updated in place; one that does not is a
// third-party status line and is preserved unless force is set.
func InstallStatusLine(settingsPath, exe string, force bool) error {
	settings := map[string]any{}
	if b, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(b, &settings); err != nil {
			return err // never overwrite a file we cannot parse
		}
	}
	if cmd := existingStatusCommand(settings); cmd != "" && !force && !strings.Contains(cmd, exe) {
		return &ErrCustomStatusLine{Command: cmd}
	}
	settings["statusLine"] = map[string]any{
		"type":            "command",
		"command":         exe,
		"refreshInterval": 10,
	}
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return err
	}
	// Write-then-rename: settings.json is the user's, and a torn write here
	// costs them every other setting in the file, not just ours.
	tmp := settingsPath + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, settingsPath)
}
