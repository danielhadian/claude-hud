# The claude-hud wire protocol

This document is the contract between the host tool (`tools/claude-usage`) and
**any** display device. The host does not know or care what is listening: if
your device speaks this protocol, it works — any microcontroller, any display,
any language. The ESP8266 + ST7735 firmware in `firmware/` is just the
reference implementation.

## Transport

- A byte stream, in practice **USB serial at 115200 baud, 8N1** (the host's
  baud is configurable — `baud` in `config.json`).
- **Strictly one-way: host → device.** The host never reads from the port.
  Anything the device writes back is debug logging for humans running a serial
  monitor; it must not expect a response.
- ASCII lines terminated by `\n` (a lone `\r` also terminates — the reference
  firmware treats both as end-of-line).

## Framing and forward compatibility

Every message is one line: a prefix, then `|`-separated `key=value` fields.

```
@U|five=4|seven=10|sub=Max 20x|five_s=14160|seven_s=140400
@S|sessions=3|busy=2
```

Devices MUST:

- **Ignore lines whose prefix they don't recognise** — future versions may add
  new line types.
- **Ignore keys they don't recognise** within a known line — fields may be
  added over time.
- **Accept keys in any order.** The host emits a fixed order today; do not
  depend on it.
- **Treat an omitted field as "unchanged"**, keeping the previously received
  value. This is load-bearing: the host omits a rate-limit window's fields
  whenever that window is absent from its data.
- Bound their line buffer and drop overlong lines whole (the host's lines are
  well under 100 bytes; 160 is a comfortable buffer).

## `@U` — usage

| key | value | meaning |
|---|---|---|
| `five` | integer 0–100 | 5-hour window, percent used |
| `seven` | integer 0–100 | 7-day window, percent used |
| `sub` | free text | subscription tier label, e.g. `Max 20x` (may contain spaces; everything after `sub=` up to the next `\|`) |
| `five_s` | integer ≥ 0 | **seconds remaining** until the 5-hour window resets |
| `seven_s` | integer ≥ 0 | seconds remaining until the 7-day window resets |

`five`/`five_s` travel together, as do `seven`/`seven_s` — but treat each key
independently and defensively (clamp percents, cap absurd second counts).

**Count the reset times down locally.** The host sends seconds-remaining
snapshots, not formatted strings, precisely so the device can keep the
countdown truthful between pushes with its own clock. Keep an independent
baseline per window — a shared one silently skews the other window when only
one is refreshed.

## `@S` — session activity

| key | value | meaning |
|---|---|---|
| `sessions` | integer ≥ 0 | open Claude Code sessions on the host |
| `busy` | integer ≥ 0 | of those, how many are actively working right now |

**Session data expires.** The host pushes roughly every 10 seconds while any
session is open, and its fallback activity windows decay in 30–60 s. A device
SHOULD stop showing session activity after ~2 minutes without an `@S` line —
last-known dots blinking "busy" forever would be a lie. (Usage percentages
should NOT decay: there is no truthful value to decay them to, and their
countdowns already stay honest.)

## Timing and lifecycle

- Expect a push roughly every **10 s** while any Claude Code session is open,
  and **silence** when none is — the statusline is the data source, so nothing
  runs when nothing is open.
- The host may reopen the port at any time (which auto-resets many dev
  boards). Shortly after opening — it waits ~1.5 s first, so a just-reset
  board is done booting and doesn't lose the bytes — it **replays the
  last-known `@U` and `@S`** so a freshly reset device shows data instead of
  a blank screen. Render whatever arrives, whenever it arrives; never require
  a handshake.
- Before the first line arrives, show an honest "waiting for data" state.

## Example session

```
@U|five=24|seven=41|sub=Max 20x|five_s=3120|seven_s=280800   ← full update
@S|sessions=2|busy=1
@S|sessions=2|busy=2                                          ← 10 s later
@U|five=25|sub=Max 20x|five_s=3050                            ← seven_* omitted: keep prior
@S|sessions=1|busy=0
(2+ minutes of silence)                                       ← decay sessions display
```

## Implementing a device: the whole checklist

1. Read serial lines at the configured baud (default 115200).
2. Parse `@U`/`@S` as above; ignore everything else.
3. Cache values; omitted key = keep previous.
4. Tick `*_s` down locally; re-render when the displayed text changes.
5. Decay session display after ~2 min of silence.
6. Render however you like — that part is entirely yours.

That's all of it. The reference firmware's parser
(`firmware/usage_display/usage_display.ino`, `pumpSerial()` /
`parseUsageLine()` / `parseSessionLine()`) is a ~80-line worked example.
