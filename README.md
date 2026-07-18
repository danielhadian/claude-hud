# claude-hud

**Your live Claude usage on a real display on your desk — any microcontroller, any screen.**

[![CI](https://github.com/danielhadian/claude-hud/actions/workflows/ci.yml/badge.svg)](https://github.com/danielhadian/claude-hud/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Platforms](https://img.shields.io/badge/host-Linux%20%7C%20macOS%20%7C%20Windows-lightgrey)](#platform-support)

A hardware HUD for Claude Code: 5-hour and weekly limit percentages with live
countdown timers, your subscription tier, and how many Claude Code sessions
are open and working right now. A small Go binary runs as **Claude Code's
statusline** and pushes the data over USB serial to whatever device you point
it at.

```
Claude Code ──statusline JSON──► claude-usage ──local socket──► claude-usage --hold
 (rate_limits)                    (statusline)                   (owns the port)
                                       │                               │
                                       ▼                               ▼ USB serial
                                 your terminal:                  your device
                                 5h 4% · 7d 10%                  (any MCU + display)
```

**No credentials. No API calls. No WiFi. No cloud.** Claude Code already knows
your usage and hands it to its statusline over a
[documented interface](https://code.claude.com/docs/en/statusline); claude-hud
just forwards it to hardware. Unplug the device and nothing breaks — the
statusline keeps printing in your terminal.

## Two pieces, one thin contract

- **The host tool** (`tools/claude-usage`, Go) is device-agnostic. It speaks a
  [two-line serial protocol](docs/PROTOCOL.md) — plain text, one-way,
  forward-compatible. Anything that parses two line formats is a valid
  display: an ESP8266, an RP2040, an Arduino with a character LCD, a spare
  e-reader, a Python script drawing in a terminal.
- **The reference firmware** (`firmware/`) is a complete, polished
  implementation for the cheapest common combo — an ESP8266 NodeMcu and a
  1.8" ST7735 TFT, about $5 of parts — with measured performance numbers and
  every hardware trap documented. Use it as-is, adapt it, or ignore it and
  build your own from the [protocol spec](docs/PROTOCOL.md).

## Highlights

- **Zero-maintenance data path** — register once as the statusline; a
  port-holder process starts itself on demand and exits when idle. No daemon,
  no service manager, nothing to keep running.
- **Truthful when quiet** — reset countdowns tick down *on the device* from
  seconds pushed by the host, and stale session dots decay to "none" after
  2 minutes of silence instead of pulsing forever.
- **Fast reference renderer** — the ESP8266 firmware pushes pixels through the
  hardware SPI FIFO (~80% of the bus ceiling, 65 fps full-screen, measured),
  with dirty-region rendering so a data tick repaints only what changed.
- **Defensive by contract** — the statusline path always prints a line and
  always exits 0, whatever fails. The panel is a nice-to-have; your terminal
  is not.

## Quick start (reference build)

The steps below assume the reference hardware. **Different board or display?**
See [Bring your own hardware](#bring-your-own-hardware) — step 2 is identical
no matter what you build.

### Parts

| Part | Notes |
|---|---|
| ESP8266 dev board (NodeMcu-style) | Any board with a USB-UART bridge — CP2102, CH340, FTDI and PL2303 are all auto-detected |
| 1.8" ST7735 TFT, 128×160 | The common blue "ST7735 + SD" module; other ST7735/ST7789 variants need only a config-header edit |
| 8 jumper wires | |

### Wiring (reference module)

| Display | NodeMcu | |
|---|---|---|
| `1-RST` | `D4` | |
| `2-CS`  | `D8` | |
| `3-D/C` | `D3` | |
| `4-DIN` | `D7` | MOSI — fixed by the SPI hardware |
| `5-CLK` | `D5` | SCLK — fixed by the SPI hardware |
| `6-VCC` | **`Vin`** | **5 V, not 3V3** on the blue module — see below |
| `7-BL`  | `3V3` | |
| `8-GND` | `GND` | |

Pins are defined in one place (`firmware/lib/Panel/src/Panel.h`) if your board
routes differently.

> [!WARNING]
> On the common blue "ST7735 + SD" module, **`VCC` goes to `Vin` (5 V), not
> `3V3`**: the module regulates VCC down itself, and 3.3 V in leaves the
> backlight too starved to light. **It looks exactly like a dead display** —
> flashes on at power-up, then dark. [docs/HARDWARE.md](docs/HARDWARE.md) has
> the full story, plus two library bugs that each cost an afternoon.

### 1. Flash the firmware

Prerequisites: [`arduino-cli`](https://arduino.github.io/arduino-cli/) on your
PATH (or in `~/.local/bin`).

```bash
cd firmware
make deps     # first time only: esp8266 core + Adafruit ST7735 library
make usage    # compile + flash; auto-detects the serial port
```

On Windows, flash from WSL2 (with
[usbipd-win](https://github.com/dorssel/usbipd-win) passing the USB device
through), or run the two `arduino-cli` commands from the Makefile manually
with `-p COMx`.

### 2. Register the host tool

Prerequisites: [Go](https://go.dev/dl/) 1.25+.

```bash
cd tools/claude-usage
go build -o claude-usage .        # claude-usage.exe on Windows
./claude-usage --install "Max 20x"   # your plan: "Pro", "Max 5x", "Max 20x", ...
```

That's the whole setup. `--install` merges a `statusLine` block into
`~/.claude/settings.json` (leaving every other setting alone) and stores your
tier label. From then on, every Claude Code session pushes usage to the device
automatically — open one and the display fills in within ~10 seconds.

> [!NOTE]
> Keep the built binary where you built it (or move it first, then re-run
> `--install`): the statusline registration points at the binary's absolute
> path.

## Bring your own hardware

Three tiers of "different", from smallest to largest:

1. **Same ST7735 family, different size or tab** — edit `PANEL_W`, `PANEL_H`,
   `PANEL_TAB` (and `PANEL_ROTATION` for mounting) in
   `firmware/lib/Panel/src/Panel.h`. The layout derives from the panel
   dimensions where that's safe.
2. **Any other Adafruit-GFX-supported display** (ST7789, ILI9341, SSD1306
   OLED, …) — swap the base class in `Panel.h`/`Panel.cpp`; the sketches draw
   only through the portable Adafruit_GFX API, so they compile unchanged. The
   file's header comment walks through it.
3. **Entirely different platform** — RP2040, ESP32, a desktop widget, a
   character LCD, CircuitPython, anything that can read a serial line:
   implement the [wire protocol](docs/PROTOCOL.md). It is two text line
   formats and a checklist of six behaviours; the spec includes a worked
   example. The host tool needs zero changes — set `baud` in `config.json` if
   your device wants something other than 115200.

If you build one, open a PR or an issue — ports and photos are the most
welcome contributions there are.

## What the reference firmware shows

- **5-HOUR** and **WEEKLY** usage %, each with a meter and a "resets in …"
  countdown that ticks down on the device itself
- your **subscription tier** (Pro / Max 5x / Max 20x / …)
- **session activity** — one dot per open Claude Code session: filled and
  pulsing for busy, hollow for idle, decaying to "none" when nothing has
  reported for 2 minutes

## Configuration

`config.json` lives in the OS config dir —
Linux: `~/.config/claude-usage/`, macOS: `~/Library/Application Support/claude-usage/`,
Windows: `%AppData%\claude-usage\`:

```json
{
  "tier": "Max 20x",
  "port": "auto",
  "baud": 115200
}
```

- **`tier`** — the label on the display. Not part of the statusline payload,
  so it's set at install (`--install "Pro"`) and never guessed; a bare
  `--install` defaults it to `"Max 20x"`.
- **`port`** — `"auto"` scans for USB-UART bridges: `/dev/ttyUSB*`,
  `/dev/ttyACM*` and the specific `cu.*` bridge patterns (SLAB, usbserial,
  wchusbserial, usbmodem) on Linux/macOS; on Windows, COM ports matched by
  VID:PID (CP2102, CH340, FTDI, PL2303), falling back to any USB serial port.
  Set an explicit device path to pin it.
- **`baud`** — serial speed; change it only if your firmware does.

## How it works

### Where the data comes from

Claude Code passes its statusline command a JSON payload on stdin containing
`rate_limits.five_hour` / `.seven_day` (`used_percentage`, `resets_at`). That
documented payload is the **only** source — the tool reads no credentials and
calls no API.

Each session's payload is its own snapshot, refreshed when that session gets
an API response — so with several sessions open they can briefly disagree. The
host folds them into a per-window maximum (keyed by the window's reset time),
so a session holding a stale snapshot can never drag the display backwards.

Session activity isn't in the payload, so it's read from Claude Code's own
session registry (`~/.claude/sessions/*.json`), which knows real busy/idle
state. That registry is undocumented, so on any doubt the tool falls back to
inferring activity from cost movement across statusline invocations — an
approximation, and a deliberately second-choice one.

### The wire protocol

One compact line per signal; the device parses, caches, and renders:

```
@U|five=4|seven=10|sub=Max 20x|five_s=14160|seven_s=140400
@S|sessions=3|busy=2
```

`five_s`/`seven_s` are **seconds remaining** until each limit resets — the
device counts them down locally, so the display stays truthful even when
nothing is pushing. A window's fields are **omitted** when that rate limit is
absent from the payload, and omission means "unchanged". Full spec, including
forward-compatibility rules and a device-implementation checklist:
[docs/PROTOCOL.md](docs/PROTOCOL.md).

### Why there's a holder process

**Opening the serial port resets most dev boards** (auto-reset circuitry;
measured 10/10 on the reference board, and disabling `HUPCL` does not help —
`firmware/scripts/reset_probe.py` proves it). A statusline command is
short-lived and runs every few seconds; if it opened the port itself, the
device would reboot forever.

So `claude-usage --hold` opens the port **once** and holds it, and statusline
invocations hand it lines over a local socket. The holder:

- takes an exclusive file lock **before** opening the port, closing the race
  where two holders would interleave writes to the device
- starts on demand when nothing is listening (rate-limited to one attempt per
  60 s, so a machine with no device attached doesn't fork endlessly)
- **replays the last-known values the instant it opens the port**, so a
  just-reset device shows data immediately instead of "waiting for data"
- exits when a write fails (device unplugged) or after 5 minutes idle,
  releasing the port for reflashing

## Platform support

| | Linux | macOS | Windows |
|---|---|---|---|
| Host tool | ✅ | ✅ | ✅ (Windows 10 1803+)¹ |
| Flashing the reference firmware | ✅ `make usage` | ✅ `make usage` | via WSL2 or manual `arduino-cli` |
| Port detection | glob (`ttyUSB*`, `ttyACM*`) | glob (bridge `cu.*` patterns) | VID:PID match, USB fallback |

¹ The statusline↔holder socket uses `AF_UNIX`, available on Windows 10 1803+.
The Windows port is CI-verified (build + vet) but has seen less real-hardware
time than Linux/macOS — issue reports welcome.

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| Backlight flashes then dark (blue ST7735 module) | `VCC` wired to `3V3` — move it to `Vin` (5 V). See the warning above. |
| `permission denied` on the port (Linux) | `sudo usermod -aG dialout $USER`, then log out/in. |
| `Device or resource busy` when flashing | The holder owns the port. `make usage` stops it automatically; from elsewhere, wait 5 min (idle exit) or kill `claude-usage --hold`. |
| Display says "waiting for data" | Nothing has pushed yet — open a Claude Code session. The statusline is the data source, so something has to run. |
| Display upside down | Physical mount orientation. Change `PANEL_ROTATION` in `firmware/lib/Panel/src/Panel.h` (0–3) and reflash. |
| Wrong port auto-detected | Pin it: set `"port"` in `config.json`, and flash with `make usage PORT=/dev/...`. |
| Usage seems stale | Usage spent in the browser (claude.ai) counts against the same limits but only reaches the display when some Claude Code session reports it. |

## Development

```bash
# Host tool — all tests are hermetic (temp dirs + fakes; no hardware needed)
cd tools/claude-usage
gofmt -l . && go vet ./... && go test ./... -race

# Reference firmware — compile everything without flashing
cd firmware
make build-all

# Diagnostics firmware
make selftest   # end-to-end hardware verification
make probe      # measures the panel's actual capabilities
make monitor    # serial monitor @115200
```

Layout:

```
tools/claude-usage/     host tool: statusline + port holder (Go; device-agnostic)
firmware/
  usage_display/        reference firmware (make usage)
  lib/Panel/            display driver — the single hardware configuration point
  examples/             selftest, probe, logo demos
  scripts/              SVG→RGB565 rasterisers, reset_probe.py, panel preview
docs/PROTOCOL.md        the wire protocol — implement this to use ANY hardware
docs/HARDWARE.md        reference build: every measured number, and the traps
docs/design/            design rationale (spec.md) and build history (build-plan.md)
```

The reference firmware's SPI bus is write-only (no MISO), so self-tests prove
the MCU side only — **only eyes confirm the picture**. When you need ground
truth on what the device received, watch its serial echo: `make monitor`.

Contributions welcome — especially ports to other boards and displays, and
real-hardware reports from macOS and Windows. Please run the commands above
before opening a PR; CI runs them too, plus cross-compiles for all five
supported platforms.

## License

[MIT](LICENSE)
