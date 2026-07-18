package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	Tier string `json:"tier"` // e.g. "Max 20x"; not in the statusline payload
	Port string `json:"port"` // "auto" or an explicit device path
	Baud int    `json:"baud"` // serial baud rate; 115200 unless your firmware differs
}

func defaults() Config { return Config{Tier: "", Port: "auto", Baud: 115200} }

// ConfigDir is the per-user config dir for this tool, in each OS's native
// location: ~/.config/claude-usage on Linux (honours XDG_CONFIG_HOME),
// ~/Library/Application Support/claude-usage on macOS, %AppData%\claude-usage
// on Windows.
func ConfigDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return "."
	}
	return filepath.Join(base, "claude-usage")
}

// LoadConfig never fails: a missing or corrupt file yields defaults, because
// this runs inside the statusline and must not break it.
func LoadConfig(dir string) Config {
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return defaults()
	}
	c := defaults()
	if err := json.Unmarshal(b, &c); err != nil {
		return defaults()
	}
	if c.Port == "" {
		c.Port = "auto"
	}
	if c.Baud <= 0 {
		c.Baud = 115200
	}
	return c
}

func SaveConfig(dir string, c Config) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), append(b, '\n'), 0o644)
}
