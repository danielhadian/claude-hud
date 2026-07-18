package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// InstallStatusLine merges a statusLine block into settings.json, preserving
// every other key. refreshInterval=10 keeps idle sessions reporting so they do
// not fall out of the session count (see sessions.go).
func InstallStatusLine(settingsPath, exe string) error {
	settings := map[string]any{}
	if b, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(b, &settings); err != nil {
			return err // never overwrite a file we cannot parse
		}
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
	return os.WriteFile(settingsPath, append(b, '\n'), 0o644)
}
