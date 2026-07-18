# Statusline-driven usage display — design

**Date:** 2026-07-16
**Status:** approved in brainstorming, pending spec review
**Supersedes:** the Python polling agent (`agent/claude_usage_agent.py`)

## Goal

Plug the board into any laptop that has Claude Code and have the panel show live
usage, with no per-laptop service install, no credentials, and no network calls.

The hardware sets a floor that cannot be removed: the ESP8266 sits behind a
CP2102 USB-UART bridge (`10c4:ea60`, `ID_USB_INTERFACES=:ff0000:`) and has no USB
peripheral of its own. It can never enumerate as mass storage, HID, or Ethernet,
so it can never cause code to run on a laptop it is plugged into. "Zero install"
is impossible; "one command, then zero touch" is the target.

## Why replace the agent

The current agent reads the OAuth access token and polls `GET /api/oauth/usage`.
That design has four defects, all verified this session:

1. **macOS incompatible.** Credentials live in the login Keychain on macOS, not
   `~/.claude/.credentials.json`. A daemon reading the Keychain also triggers an
   access prompt — a per-laptop interaction, exactly where zero-touch mattered.
2. **ToS exposure.** A Feb 2026 policy restricts OAuth tokens to official
   Anthropic clients; third-party use is stated to violate Consumer ToS.
3. **Undocumented dependency.** `/api/oauth/usage` is not in the published API
   docs and can change without notice. So is `~/.claude/sessions/*.json`.
4. **A whole bug class.** Token expiry → 401 → retry → 429 → backoff. This blanked
   the panel today (see `tasks/todo.md`, 2026-07-16).

Claude Code hands us the same data on a **documented** interface, for free.

## Verified facts this design rests on

| # | Fact | Evidence |
|---|------|----------|
| 1 | statusline stdin carries `rate_limits.{five_hour,seven_day}.{used_percentage, resets_at}` | code.claude.com/docs/en/statusline |
| 2 | `rate_limits` appears only for Pro/Max subscribers, and only **after the session's first API response** | ibid |
| 3 | No tier-label field and no busy/active field exist in the payload | ibid |
| 4 | Cadence: after each assistant message, after `/compact`, on permission-mode change, on vim toggle; debounced 300 ms; plus optional `refreshInterval` (min 1 s) | ibid |
| 5 | **Every** serial open resets the board; `-hupcl` does not help — measured 10/10 reboots | `tmp/reset_probe.py`, this session |
| 6 | A `setsid`'d child survives `SIGKILL` of its spawner's whole process group — spawner confirmed dead, child kept running | `tmp/spawn_spike2.sh`, this session |

Fact 5 is why a holder process is unavoidable. Fact 6 is why it needs no service manager.

## Architecture

```
Claude Code session(s)
   │  statusline JSON on stdin
   ▼
claude-usage            (short-lived, per invocation)
   ├─→ stdout: statusline text        (Claude Code renders it)
   └─→ socket ──┐
                ▼
        claude-usage --hold           (self-spawned, setsid, one per machine)
        owns the serial port for its lifetime
                │
                ▼  USB serial @115200, opened ONCE
          ESP8266 → Panel
```

The statusline binary never touches the serial port, so the board resets at most
once per holder lifetime rather than once per invocation.

## Components

### 1. `claude-usage` binary (Go)

Go over Rust: `GOOS=windows go build` cross-compiles all three targets from the
Linux laptop with no cross toolchain, and produces a static binary with no libc
dependency. One pure-Go dependency (`go.bug.st/serial`) — not stdlib-only, but no
cgo, so cross-compilation stays trivial.

Modes:

- **(no args)** — statusline mode. Read JSON from stdin, print the statusline to
  stdout, forward one `@U`/`@S` line to the holder socket, exit.
- **`--hold`** — holder mode. Own the port, serve the socket.
- **`--install`** — write the `statusLine` block into `settings.json`, set tier.
- **`--version`**

### 2. Holder

- Socket: `$XDG_RUNTIME_DIR/claude-usage.sock` (Linux), `$TMPDIR` (macOS), named
  pipe `\\.\pipe\claude-usage` (Windows).
- Single instance enforced by an exclusive lock on a sidecar lockfile; losing the
  race means another holder is live, so just connect to it.
- Opens serial once: 115200 8N1, **DTR and RTS deasserted** (`docs/HARDWARE.md`).
- Accepts newline-delimited lines, writes through to serial, last-writer-wins.
  Rate limits are account-wide, so any session's numbers are valid for all —
  no cross-session reconciliation needed, and no lock around the write.
- **Idle timeout: 5 minutes** with no input → exit and close the port. This
  releases `/dev/ttyUSB0` for `make usage`, retiring the "stop the agent before
  flashing" gotcha.

Spawning: statusline mode connects to the socket; on ECONNREFUSED/ENOENT it
spawns itself `--hold` detached (`setsid` on POSIX, `DETACHED_PROCESS` on
Windows), then drops this invocation's line. The next invocation lands normally.
One skipped line is invisible at statusline cadence.

### 3. Wire format

Unchanged prefixes, one changed field group:

```
@U|five=<int 0-100>|seven=<int 0-100>|sub=<label>|five_s=<int>|seven_s=<int>
@S|sessions=<int>|busy=<int>
```

`five_s`/`seven_s` are **seconds remaining** until reset, replacing today's
preformatted `fivereset=52m`. The host computes them from `resets_at` (epoch) minus
now; the firmware counts them down locally. `@S` is unchanged.

Percentages are rounded to int on the host — the payload gives floats (e.g. 23.5),
the panel renders integers.

### 4. Firmware changes (`usage_display.ino`)

- `parseUsageLine()`: accept `five_s`/`seven_s` instead of `fivereset`/`sevenreset`.
- Store each reset's seconds plus a `millis()` baseline at receipt.
- Each frame, compute `remaining = base_s - (millis() - base_ms)/1000`, clamp at 0,
  and format `"52m"` / `"3d 4h"` on-device.
- Re-render the reset text only when the **formatted string** changes (roughly once
  a minute), via the existing `UD_VALS` dirty region. No new full-screen repaints —
  the dirty-region contract in CLAUDE.md stands.
- The board has no RTC; `millis()` drift over a 7-day window is irrelevant at
  minute display granularity.

This makes the countdown correct even when no Claude Code session is open, which
the current preformatted-string design cannot do.

### 5. Sessions and busy, from documented data only

The payload has no busy flag, so infer it. State dir, one small file per
`session_id`: `{last_seen_unix, last_cost_usd}`.

- `sessions` = files with `last_seen` within 60 s
- `busy` = files whose `cost.total_cost_usd` changed within the last 30 s

This requires `refreshInterval` (set to 10 s by `--install`) so idle sessions keep
reporting and don't fall out of the count. Stale files older than 10 minutes are
pruned on each run.

Accepted behaviour change: dots pulse on cost movement rather than on a `busy`
flag, and a session that is open but silent for >60 s drops out of the count.

### 6. Tier label

Not in the payload. Read once at `--install` and stored in
`~/.config/claude-usage/config.json`: `{"tier": "Max 20x", "port": "auto"}`.
It changes approximately never; a wrong label is cosmetic.

## Error handling

The binary runs inside the user's statusline. **It must never hang, crash, or
delay Claude Code.** Every failure is fail-open:

| Condition | Behaviour |
|-----------|-----------|
| Board unplugged / port missing | Print statusline, skip send, exit 0 |
| Holder unreachable and spawn fails | Print statusline, exit 0 |
| Socket write blocks | 100 ms timeout, then abandon the line |
| `rate_limits` absent (pre-first-response, or non-subscriber) | Send no `@U`; board keeps its last value |
| Malformed/absent stdin JSON | Print a minimal statusline, exit 0 |
| Holder cannot open serial | Log to its own file, keep serving, retry each write |

Exit code is always 0 in statusline mode. The panel is a nice-to-have; the
terminal is not.

## Testing

- **Pure core:** table tests over JSON fixtures → expected `@U`/`@S` line. Covers
  rounding, missing `rate_limits`, absent windows, clamping, tier substitution.
- **Holder:** point it at a PTY instead of a real port; assert bytes written and
  that a second instance defers to the first.
- **Session inference:** synthetic state dirs → expected `sessions`/`busy`.
- **Regression:** move this session's `reset_probe.py` into `firmware/scripts/`
  (it currently lives in a scratch dir that is deleted with the job). It proves
  fact 5 still holds and would catch a regression where the holder reopens the
  port per write instead of holding it.
- **Firmware countdown:** feed `five_s=3600`, advance, assert the rendered string
  transitions 60m → 59m and clamps at 0.
- **Eyes-on:** SPI is write-only; only the user can confirm the picture.

## Retirement of the Python agent

Remove `agent/claude_usage_agent.py`, `install.sh`, `requirements.txt`, and the
`claude-usage` systemd unit. Uninstall on this laptop:
`systemctl --user disable --now claude-usage` then delete the unit.

The 401 fix made today stays in `tasks/todo.md` as the diagnosis record; the code
it fixed goes away with the agent. Update `README.md`, `agent/README.md`, and
`CLAUDE.md` (the "One transport" and "Commands" sections describe the agent).

## Limitations (accepted)

- **Browser usage is invisible.** Usage from claude.ai or another machine counts
  against the same limits but reaches the board only if a Claude Code session is
  open somewhere to report it. This is the real cost of dropping the API poll.
- **One visible reboot per work session.** Because every open resets the board
  (fact 5), the panel reboots when a holder starts. `refreshInterval` keeps the
  holder alive while any session is open, so this happens only when starting
  Claude Code more than 5 minutes after closing the last session — once per work
  session, not per invocation. The alternative (holding the port forever) would
  re-break `make usage` flashing, which is a worse trade.
- `rate_limits` is missing until a session's first API response, so a freshly
  opened session shows the previous value briefly.
- The statusline schema is documented but carries no stated stability guarantee.
- **Unverified:** detached spawn on Windows (`DETACHED_PROCESS`) and macOS. macOS
  is POSIX and `setsid` should behave as on Linux, but neither is measured. Windows
  is the higher risk and should be spiked before its install path is built.

## Out of scope

- WiFi-standalone operation (would need the refresh token in unencrypted flash and
  could rotate/invalidate the laptop's own session).
- Driving more than one board.
- Spoofing `User-Agent: claude-code/<version>` to dodge rate limiting — that
  circumvents a deliberate control, and this design makes no API calls anyway.
