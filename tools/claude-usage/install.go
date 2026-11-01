package main

import (
	"encoding/json"
	"errors"
	"fmt"
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
	return "settings.json already has a statusLine command that is not this binary: " + e.Command
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

// InstallStatusLine registers the quoted executable path, preserving unrelated
// settings. Only an exact bare-path registration belongs to us; wrappers are
// custom commands even when they invoke this binary.
func InstallStatusLine(settingsPath, exe string, force bool) error {
	settings := map[string]any{}
	if b, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(b, &settings); err != nil {
			return err // never overwrite a file we cannot parse
		}
		if settings == nil {
			return fmt.Errorf("settings.json must contain a JSON object")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	command := shellQuote(exe)
	if cmd := existingStatusCommand(settings); cmd != "" && !force && cmd != exe && cmd != command {
		return &ErrCustomStatusLine{Command: cmd}
	}
	settings["statusLine"] = map[string]any{
		"type":            "command",
		"command":         command,
		"refreshInterval": 10,
	}
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return err
	}
	return writeAtomic(settingsPath, append(b, '\n'))
}

// shellQuote protects an executable path passed to Claude Code's shell.
func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(filepath.ToSlash(path), "'", "'\"'\"'") + "'"
}
