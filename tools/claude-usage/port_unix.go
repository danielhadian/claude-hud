//go:build unix

package main

import (
	"path/filepath"
	"sort"
)

// candidateGlobs is a var, not a const, so tests can point it at nothing.
// Covers the USB-UART bridges NodeMcu boards ship with, on Linux and macOS:
// CP2102 (ttyUSB*, cu.SLAB_*, cu.usbserial-*), CH340 (ttyUSB*, cu.wchusbserial*),
// and native-USB CDC-ACM boards (ttyACM*, cu.usbmodem*).
var candidateGlobs = []string{
	"/dev/ttyUSB*",
	"/dev/ttyACM*",
	"/dev/cu.SLAB_USBtoUART*",
	"/dev/cu.usbserial-*",
	"/dev/cu.wchusbserial*",
	"/dev/cu.usbmodem*",
}

// FindPort resolves the configured port, or auto-detects the first candidate.
func FindPort(cfg Config) (string, error) {
	if cfg.Port != "" && cfg.Port != "auto" {
		return cfg.Port, nil
	}
	var found []string
	for _, g := range candidateGlobs {
		m, err := filepath.Glob(g)
		if err != nil {
			continue
		}
		found = append(found, m...)
	}
	if len(found) == 0 {
		return "", errNoPort
	}
	sort.Strings(found)
	return found[0], nil
}
