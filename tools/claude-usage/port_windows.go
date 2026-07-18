//go:build windows

package main

import (
	"strings"

	"go.bug.st/serial/enumerator"
)

// knownBridges are the USB-UART bridge chips NodeMcu-style boards ship with,
// as VID:PID. Preferring these over "any COM port" matters on Windows, where
// virtual COM ports (Bluetooth, printers, VPNs) are common and would otherwise
// win the auto-detect.
var knownBridges = map[string]bool{
	"10C4:EA60": true, // Silicon Labs CP2102 (the measured board, docs/HARDWARE.md)
	"1A86:7523": true, // WCH CH340 (most NodeMcu clones)
	"0403:6001": true, // FTDI FT232R
	"067B:2303": true, // Prolific PL2303
}

// FindPort resolves the configured port, or auto-detects: first a known
// USB-UART bridge by VID:PID, then any USB serial port, else errNoPort.
func FindPort(cfg Config) (string, error) {
	if cfg.Port != "" && cfg.Port != "auto" {
		return cfg.Port, nil
	}
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return "", err
	}
	for _, p := range ports {
		if p.IsUSB && knownBridges[strings.ToUpper(p.VID+":"+p.PID)] {
			return p.Name, nil
		}
	}
	for _, p := range ports {
		if p.IsUSB {
			return p.Name, nil
		}
	}
	return "", errNoPort
}
