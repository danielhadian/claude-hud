# ST7735 + NodeMcu (ESP8266) — build / flash / monitor
#
#   make usage        build + flash the real firmware (Claude usage display)
#   make selftest     build + flash the verification suite
#   make logo         build + flash the vector-mark demo
#   make probe        build + flash the hardware capability probe
#   make monitor      open the serial monitor (Ctrl-C to quit)
#   make build-all    compile everything without flashing
#   make erase        wipe the flash completely
#   make deps         install/refresh the toolchain and libraries
#
# PORT auto-detects the first USB-serial device on Linux and macOS; override
# with `make usage PORT=/dev/ttyUSB1`. ACLI finds arduino-cli on PATH, else
# falls back to ~/.local/bin.

ACLI  ?= $(shell command -v arduino-cli 2>/dev/null || echo $(HOME)/.local/bin/arduino-cli)
FQBN  ?= esp8266:esp8266:nodemcuv2
PORT  ?= $(shell ls /dev/ttyUSB* /dev/ttyACM* /dev/cu.SLAB_USBtoUART* /dev/cu.usbserial-* /dev/cu.wchusbserial* /dev/cu.usbmodem* 2>/dev/null | head -1)
BAUD  ?= 115200
LIBS  := --library lib/Panel
URL   := https://arduino.esp8266.com/stable/package_esp8266com_index.json

# The claude-usage holder owns the serial port while Claude Code is running.
# Flashing needs the port, so the flash recipe (a) stops any holder — the
# `[-]` bracket keeps pkill's regex from matching its own command line — and
# (b) where flock(1) exists (Linux), holds the holder's OWN lockfile during
# the upload, so a holder respawned mid-flash bails out in TryLock instead of
# stealing the port. On macOS (no flock) the pkill plus the tool's 60 s spawn
# cooldown covers the window in practice.
LOCKDIR  := $(firstword $(XDG_RUNTIME_DIR) $(TMPDIR) /tmp)
LOCKFILE := $(LOCKDIR)/claude-usage.lock

EXAMPLES := selftest logo probe

.PHONY: $(EXAMPLES) usage build-all monitor erase deps clean help

define flash
	$(ACLI) compile --fqbn $(FQBN) $(LIBS) $(1)
	-pkill -f 'claude-usage [-]-hold' 2>/dev/null; true
	@if command -v flock >/dev/null 2>&1; then \
	  flock -w 10 $(LOCKFILE) $(ACLI) upload --fqbn $(FQBN) -p $(PORT) $(1); \
	else \
	  $(ACLI) upload --fqbn $(FQBN) -p $(PORT) $(1); \
	fi
	@echo ">> flashed '$(1)' to $(PORT). Run 'make monitor' to watch Serial."
endef

# The real firmware: shows live Claude usage over USB serial.
usage:    ; $(call flash,usage_display)

selftest: ; $(call flash,examples/selftest)
logo:     ; $(call flash,examples/logo)
probe:    ; $(call flash,examples/probe)

build-all:
	@for s in $(EXAMPLES); do \
	  echo "=== compiling $$s ==="; \
	  $(ACLI) compile --fqbn $(FQBN) $(LIBS) examples/$$s || exit 1; \
	done
	@echo "=== compiling usage_display ==="
	@$(ACLI) compile --fqbn $(FQBN) $(LIBS) usage_display

monitor:
	$(ACLI) monitor -p $(PORT) --config baudrate=$(BAUD)

# Wipes everything, including any stored config. Back it up first if it matters.
erase:
	esptool --port $(PORT) --baud 460800 erase-flash

deps:
	$(ACLI) core update-index --additional-urls $(URL)
	$(ACLI) core install esp8266:esp8266 --additional-urls $(URL)
	$(ACLI) lib install "Adafruit ST7735 and ST7789 Library"

clean:
	rm -rf build

help:
	@sed -n '2,10p' $(MAKEFILE_LIST)
