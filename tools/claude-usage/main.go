// claude-usage is the host side of claude-hud: it runs as Claude Code's
// statusline, prints the usage line to the terminal, and forwards compact
// protocol lines (docs/PROTOCOL.md) to an attached display device over USB
// serial, via a self-spawned port-holder process (`--hold`).
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const version = "1.3.0"

const spawnCooldown = 60 * time.Second

// bootSettle is how long runHolder waits after opening the port before replaying
// the cache. Opening resets the reference ESP8266 (measured 10/10), which then
// spends ~1.8s in setup() delays (delay(300)+delay(1500)) before its first
// pumpSerial(); anything sent during the boot ROM that follows the reset (the
// first few hundred ms, UART RX dead) is lost. 1.5s clears that window with wide
// margin while the firmware is still mid-setup, so its RX FIFO holds the replay
// until loop() drains it. Other boards can differ, but a small over-wait only
// delays the first repaint; too little silently loses it. docs/HARDWARE.md.
const bootSettle = 1500 * time.Millisecond

func stateDir() string { return filepath.Join(ConfigDir(), "sessions") }

// maySpawn reports whether we should try to start a holder, and records the
// attempt. Without this, a machine with no board attached would fork a holder
// on EVERY statusline invocation: Send always fails, the spawned holder finds
// no port and exits immediately, and nothing remembers we just tried.
func maySpawn(dir string, now time.Time) bool {
	marker := filepath.Join(dir, "claude-usage.spawn")
	if fi, err := os.Stat(marker); err == nil && now.Sub(fi.ModTime()) < spawnCooldown {
		return false
	}
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return true // cannot track attempts; spawning is the safer failure
	}
	f.Close()
	_ = os.Chtimes(marker, now, now)
	return true
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--hold":
			runHolder()
			return
		case "--install":
			tier := ""
			if len(os.Args) > 2 {
				tier = os.Args[2]
			}
			runInstall(tier)
			return
		case "--version":
			fmt.Println("claude-usage", version)
			return
		case "--help", "-h", "help":
			fmt.Print(usageText)
			return
		default:
			if strings.HasPrefix(os.Args[1], "-") {
				// An unknown flag must not fall through to statusline mode,
				// which would silently block reading a terminal's stdin.
				fmt.Fprintf(os.Stderr, "claude-usage: unknown flag %q\n\n%s", os.Args[1], usageText)
				os.Exit(2)
			}
		}
	}
	runStatusline()
}

const usageText = `claude-usage - live Claude usage on a hardware display, fed by Claude Code's statusline (claude-hud)

usage:
  claude-usage --install [tier]   register as Claude Code's statusline (tier e.g. "Pro", "Max 5x"; default "Max 20x")
  claude-usage --version          print the version
  claude-usage --hold             hold the serial port and forward lines (started automatically; not for manual use)
  claude-usage                    statusline mode: read the payload on stdin, print the line, push to the device
`

// runStatusline is the hot path. It must ALWAYS print a line and exit 0.
func runStatusline() {
	// Last-resort insurance for the "never break Claude Code" invariant: the
	// status line is printed before any risky work below, so a recovered panic
	// still leaves Claude Code with a line and a zero exit.
	defer func() { _ = recover() }()

	var p *Payload
	// Bound the read: Claude Code closes stdin after the payload, but a stuck
	// pipe must not let an unbounded ReadAll wedge or balloon memory.
	if b, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20)); err == nil && len(b) > 0 {
		var parsed Payload
		if json.Unmarshal(b, &parsed) == nil {
			p = &parsed
		}
	}
	fmt.Println(StatusText(p)) // the statusline itself - never skipped

	if p == nil {
		return
	}
	now := time.Now()
	cfg := LoadConfig(ConfigDir())

	_ = RecordSession(stateDir(), p, now)

	// Fold this session's rate-limit snapshot into the cross-session maxima
	// (see usagestate.go): payload rate_limits are per-session and go stale,
	// and without the merge the last writer drags the device below the
	// freshest known value. Runs after the StatusText print on purpose — the
	// terminal line is this session's own context, the device shows the
	// aggregate, and no file IO happens before the must-never-fail print.
	p = MergeUsage(ConfigDir(), p)

	var lines []string
	if u, ok := UsageLine(p, cfg.Tier, now); ok {
		lines = append(lines, u)
	}
	// Prefer Claude Code's own session registry: it is the only source that sees
	// sessions which are open but idle, and the only one with a real busy flag.
	// It is undocumented, so fall back to the cost inference if it isn't there.
	if s, ok := LiveSessionLine(ClaudeSessionsDir()); ok {
		lines = append(lines, s)
	} else {
		lines = append(lines, SessionLine(stateDir(), now))
	}
	if len(lines) == 0 {
		return
	}

	sock := SocketPath()
	for _, l := range lines {
		if err := Send(sock, l, 100*time.Millisecond); err != nil {
			// No holder: spawn one and drop this line. The next invocation
			// lands normally - one skipped line is invisible at this cadence.
			// Gated by maySpawn so a machine with no board attached does not
			// fork a new holder on every single invocation.
			if maySpawn(runtimeDir(), time.Now()) {
				if exe, e := os.Executable(); e == nil {
					_ = SpawnHolder(exe)
				}
			}
			return
		}
	}
}

// replayCache pushes the last-known @U/@S lines to a freshly opened port so a
// reconnected/rebooted board repaints immediately instead of sitting on
// "connecting" until the next live push. It waits `settle` FIRST: OpenSink just
// reset the auto-reset board (every open resets it, measured 10/10 -
// docs/HARDWARE.md), and bytes sent while it is still in its boot ROM never
// reach the sketch's UART. Waiting out the boot is the whole point.
func replayCache(sink Sink, dir string, settle time.Duration) {
	time.Sleep(settle) // let the just-reset board finish booting before we send
	for _, l := range loadCache(dir) {
		_ = fullWrite(sink, []byte(l+"\n"))
	}
}

func runHolder() {
	// Take the lock BEFORE opening the port. The flock is the primary guard and
	// must be taken before FindPort/OpenSink so it covers the detect->open window
	// (go.bug.st/serial does set TIOCEXCL, but only after open, so it can't guard
	// that window); without it two holders would race to open /dev/ttyUSB0 and
	// interleave writes. Use `return`, never os.Exit: os.Exit skips defers and
	// would leak the lock.
	release, ok, err := TryLock(LockPath())
	if err != nil || !ok {
		return // another holder already owns the port
	}
	defer release()

	cfg := LoadConfig(ConfigDir())
	dev, err := FindPort(cfg)
	if err != nil {
		return // no board plugged in - nothing to hold
	}
	sink, err := OpenSink(dev, cfg.Baud)
	if err != nil {
		return
	}

	// Opening the port just reset the board, so it has no data and is showing
	// "waiting for data" / "connecting". Replay the last-known values so the
	// panel repaints without waiting for the next statusline push — on a
	// reconnect/reboot with no active Claude Code turn, that push may not come
	// for a while. replayCache waits out the board's boot first (see there).
	dir := ConfigDir()
	replayCache(sink, dir, bootSettle)

	_ = Serve(SocketPath(), &cachingSink{Sink: sink, dir: dir}, 5*time.Minute)
}

func runInstall(tier string) {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot find home dir:", err)
		os.Exit(1)
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot find own path:", err)
		os.Exit(1)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := InstallStatusLine(settings, exe); err != nil {
		fmt.Fprintln(os.Stderr, "install failed:", err)
		os.Exit(1)
	}
	cfg := LoadConfig(ConfigDir())
	if tier != "" {
		cfg.Tier = tier // explicit --install argument wins
	} else if cfg.Tier == "" {
		cfg.Tier = "Max 20x" // not in the payload; see the message below to change
	}
	if err := SaveConfig(ConfigDir(), cfg); err != nil {
		fmt.Fprintln(os.Stderr, "config save failed:", err)
		os.Exit(1)
	}
	fmt.Println("✓ statusLine registered ->", exe)
	fmt.Printf("✓ tier label: %q  (change: claude-usage --install \"Pro\", or edit %s)\n",
		cfg.Tier, filepath.Join(ConfigDir(), "config.json"))
	fmt.Println("✓ done - open (or continue) any Claude Code session and the panel updates within ~10s")
}
