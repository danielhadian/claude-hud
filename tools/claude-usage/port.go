package main

import (
	"errors"

	"go.bug.st/serial"
)

var errNoPort = errors.New("no serial port found")

// OpenSink opens the port with DTR and RTS DEASSERTED. Asserting them drives
// auto-reset boards (e.g. the reference ESP8266) into their UART bootloader
// (docs/HARDWARE.md). The open may still reset the board once - unavoidable,
// measured - which is exactly why the holder keeps it open instead of
// reopening per line.
func OpenSink(dev string, baud int) (Sink, error) {
	port, err := serial.Open(dev, &serial.Mode{
		BaudRate: baud,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	})
	if err != nil {
		return nil, err
	}
	if err := port.SetDTR(false); err != nil {
		port.Close()
		return nil, err
	}
	if err := port.SetRTS(false); err != nil {
		port.Close()
		return nil, err
	}
	return port, nil
}
