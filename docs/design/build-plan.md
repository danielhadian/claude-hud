# Statusline Usage Display Implementation Plan

> **Historical document** (2026-07-16): the step-by-step plan the initial
> implementation followed. Kept for the reasoning; the shipped code has since
> evolved (e.g. Windows support, originally deferred here, now exists). The
> companion design rationale is [spec.md](spec.md).

**Goal:** Replace the credential-reading, API-polling Python agent with a Go binary that runs as Claude Code's statusline and feeds the ESP8266 through a self-spawned process that holds the serial port open.

**Architecture:** Claude Code pipes documented statusline JSON to `claude-usage` on stdin. It prints the statusline to stdout and forwards one `@U`/`@S` line to a Unix socket. A self-spawned `claude-usage --hold` owns the serial port for its lifetime and writes through to the board — necessary because *every* port open resets the ESP8266 (measured 10/10). No credentials, no network, no service manager.

**Tech Stack:** Go 1.26.5 (`/snap/bin/go`), `go.bug.st/serial` (pure Go, no cgo), Arduino/C++ via `arduino-cli` 1.5.1, FQBN `esp8266:esp8266:nodemcuv2`.

**Spec:** [`docs/design/spec.md`](spec.md)

## Global Constraints

- **Scope is Linux + macOS.** Windows is explicitly deferred: the spec records detached spawn on Windows (`DETACHED_PROCESS`) as unverified and requires a spike before its install path is built. Unix-specific files use `//go:build unix`. The pure core must stay platform-neutral so a later Windows plan only adds files.
- **Fail-open, always.** The binary runs inside the user's statusline. Any error → still print a statusline, exit 0. Never hang, never panic, never non-zero in statusline mode.
- **Serial port opens with DTR and RTS deasserted** (`docs/HARDWARE.md`) — asserting them drives the ESP8266 into the UART bootloader.
- **Port:** `/dev/ttyUSB0`, baud `115200`, 8N1.
- **Only one process may own the port.** Stop the old agent before any hardware test: `systemctl --user stop claude-usage`.
- **Wire format is newline-terminated ASCII**, `@U|k=v|k=v\n` and `@S|k=v|k=v\n`.
- **Do not reintroduce a full `fillScreen` on data change** — the dirty-region contract in `CLAUDE.md` stands.
- Go module path: `github.com/danielhadian/esp8266-tft/tools/claude-usage` *(historical — the module is now `github.com/danielhadian/claude-hud/tools/claude-usage`)*. Build: `cd tools/claude-usage && go build -o claude-usage .`

## File Structure

| File | Responsibility |
|------|----------------|
| `tools/claude-usage/go.mod` | Module + `go.bug.st/serial` dependency |
| `tools/claude-usage/payload.go` | The statusline stdin JSON struct |
| `tools/claude-usage/wire.go` | Payload → `@U` line (pure) |
| `tools/claude-usage/render.go` | Payload → statusline text (pure) |
| `tools/claude-usage/config.go` | Tier label + port override |
| `tools/claude-usage/sessions.go` | Session state dir → `@S` line |
| `tools/claude-usage/holder.go` | Socket server owning the serial port |
| `tools/claude-usage/client.go` | Send a line; spawn holder if absent |
| `tools/claude-usage/spawn_unix.go` | `setsid` detached spawn (`//go:build unix`) |
| `tools/claude-usage/port_unix.go` | Port discovery (`//go:build unix`) |
| `tools/claude-usage/install.go` | Write `statusLine` into `settings.json` |
| `tools/claude-usage/main.go` | Mode dispatch |
| `firmware/usage_display/usage_display.ino` | Local countdown from seconds-remaining |
| `firmware/scripts/reset_probe.py` | Regression probe for the reset fact |

---

### Task 1: Wire format — payload to `@U` line

The pure heart of the system. Everything else is plumbing around this.

**Files:**
- Create: `tools/claude-usage/go.mod`
- Create: `tools/claude-usage/payload.go`
- Create: `tools/claude-usage/wire.go`
- Test: `tools/claude-usage/wire_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Payload`, `type Window`, `func UsageLine(p *Payload, tier string, now time.Time) (string, bool)`.

- [ ] **Step 1: Create the module**

```bash
mkdir -p tools/claude-usage && cd tools/claude-usage
go mod init github.com/danielhadian/esp8266-tft/tools/claude-usage
```

- [ ] **Step 2: Write `payload.go`**

Only the fields we consume. Pointers on `RateLimits`/`Window` because the spec records
each as independently absent (missing until a session's first API response).

```go
package main

// Payload is the subset of Claude Code's statusline stdin JSON we use.
// Schema: https://code.claude.com/docs/en/statusline
type Payload struct {
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	SessionID  string      `json:"session_id"`
	RateLimits *RateLimits `json:"rate_limits"`
	Cost       struct {
		TotalCostUSD float64 `json:"total_cost_usd"`
	} `json:"cost"`
}

// RateLimits is absent entirely until a session's first API response, and for
// non-subscribers. Each window is independently absent, hence the pointers.
type RateLimits struct {
	FiveHour *Window `json:"five_hour"`
	SevenDay *Window `json:"seven_day"`
}

// Window is one rate-limit window. ResetsAt is Unix epoch seconds.
type Window struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       int64   `json:"resets_at"`
}
```

- [ ] **Step 3: Write the failing test `wire_test.go`**

```go
package main

import (
	"testing"
	"time"
)

func at(sec int64) time.Time { return time.Unix(sec, 0) }

func p(five, seven *Window) *Payload {
	return &Payload{RateLimits: &RateLimits{FiveHour: five, SevenDay: seven}}
}

func TestUsageLine(t *testing.T) {
	tests := []struct {
		name string
		in   *Payload
		tier string
		now  time.Time
		want string
		ok   bool
	}{
		{
			name: "both windows",
			in:   p(&Window{23.5, 1000 + 3120}, &Window{41.2, 1000 + 280800}),
			tier: "Max 20x", now: at(1000),
			want: "@U|five=24|seven=41|sub=Max 20x|five_s=3120|seven_s=280800\n",
			ok:   true,
		},
		{
			name: "five only - absent seven is omitted, board keeps prior",
			in:   p(&Window{10.0, 1000 + 60}, nil),
			tier: "Max 20x", now: at(1000),
			want: "@U|five=10|sub=Max 20x|five_s=60\n",
			ok:   true,
		},
		{
			name: "past reset clamps to zero, never negative",
			in:   p(&Window{99.9, 500}, nil),
			tier: "Pro", now: at(1000),
			want: "@U|five=100|sub=Pro|five_s=0\n",
			ok:   true,
		},
		{
			name: "no rate_limits at all - nothing to send",
			in:   &Payload{},
			tier: "Max 20x", now: at(1000),
			want: "", ok: false,
		},
		{
			name: "rate_limits present but both windows nil",
			in:   p(nil, nil),
			tier: "Max 20x", now: at(1000),
			want: "", ok: false,
		},
		{
			name: "empty tier omits the sub field",
			in:   p(&Window{5.0, 1000 + 120}, nil),
			tier: "", now: at(1000),
			want: "@U|five=5|five_s=120\n",
			ok:   true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := UsageLine(tc.in, tc.tier, tc.now)
			if ok != tc.ok || got != tc.want {
				t.Errorf("UsageLine() = %q,%v; want %q,%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}
```

- [ ] **Step 4: Run it and confirm it fails**

Run: `cd tools/claude-usage && go test ./... -run TestUsageLine -v`
Expected: FAIL — `undefined: UsageLine`

- [ ] **Step 5: Write `wire.go`**

```go
package main

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// pct rounds a 0-100 float to an int and clamps it.
func pct(f float64) int {
	v := int(math.Round(f))
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// secsUntil returns whole seconds from now to epoch, never negative.
func secsUntil(epoch int64, now time.Time) int64 {
	d := epoch - now.Unix()
	if d < 0 {
		return 0
	}
	return d
}

// UsageLine renders the @U line. Absent windows are omitted entirely: the
// firmware's parseUsageLine keeps prior values for fields it does not see, so
// omission is how we say "unchanged". Returns ok=false when there is nothing
// worth sending, in which case the board keeps its last value.
func UsageLine(p *Payload, tier string, now time.Time) (string, bool) {
	if p == nil || p.RateLimits == nil {
		return "", false
	}
	five, seven := p.RateLimits.FiveHour, p.RateLimits.SevenDay
	if five == nil && seven == nil {
		return "", false
	}

	parts := []string{"@U"}
	if five != nil {
		parts = append(parts, fmt.Sprintf("five=%d", pct(five.UsedPercentage)))
	}
	if seven != nil {
		parts = append(parts, fmt.Sprintf("seven=%d", pct(seven.UsedPercentage)))
	}
	if tier != "" {
		parts = append(parts, "sub="+tier)
	}
	if five != nil {
		parts = append(parts, fmt.Sprintf("five_s=%d", secsUntil(five.ResetsAt, now)))
	}
	if seven != nil {
		parts = append(parts, fmt.Sprintf("seven_s=%d", secsUntil(seven.ResetsAt, now)))
	}
	return strings.Join(parts, "|") + "\n", true
}
```

- [ ] **Step 6: Run tests, confirm pass**

Run: `cd tools/claude-usage && go test ./... -run TestUsageLine -v`
Expected: PASS, all six subtests.

- [ ] **Step 7: Checkpoint (no commit — see Global Constraints)**

Run: `cd tools/claude-usage && go vet ./... && go test ./...`
Expected: no vet output, `ok` from test.

---

### Task 2: Statusline text

The command's stdout **is** the statusline Claude Code renders, so this is a real
deliverable, not a debug aid.

**Files:**
- Create: `tools/claude-usage/render.go`
- Test: `tools/claude-usage/render_test.go`

**Interfaces:**
- Consumes: `Payload`, `Window` (Task 1).
- Produces: `func StatusText(p *Payload) string`.

- [ ] **Step 1: Write the failing test**

```go
package main

import "testing"

func TestStatusText(t *testing.T) {
	tests := []struct {
		name string
		in   *Payload
		want string
	}{
		{
			name: "both windows render both percentages",
			in:   p(&Window{23.5, 0}, &Window{41.2, 0}),
			want: "5h 24% · 7d 41%",
		},
		{
			name: "five only",
			in:   p(&Window{7.0, 0}, nil),
			want: "5h 7%",
		},
		{
			name: "no rate limits yet - say so rather than lie",
			in:   &Payload{},
			want: "usage —",
		},
		{
			name: "nil payload never panics",
			in:   nil,
			want: "usage —",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := StatusText(tc.in); got != tc.want {
				t.Errorf("StatusText() = %q; want %q", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run it, confirm failure**

Run: `cd tools/claude-usage && go test ./... -run TestStatusText -v`
Expected: FAIL — `undefined: StatusText`

- [ ] **Step 3: Write `render.go`**

```go
package main

import (
	"fmt"
	"strings"
)

// StatusText is what Claude Code shows in the status line. Must never panic:
// it runs on every assistant message.
func StatusText(p *Payload) string {
	if p == nil || p.RateLimits == nil {
		return "usage —"
	}
	var parts []string
	if f := p.RateLimits.FiveHour; f != nil {
		parts = append(parts, fmt.Sprintf("5h %d%%", pct(f.UsedPercentage)))
	}
	if s := p.RateLimits.SevenDay; s != nil {
		parts = append(parts, fmt.Sprintf("7d %d%%", pct(s.UsedPercentage)))
	}
	if len(parts) == 0 {
		return "usage —"
	}
	return strings.Join(parts, " · ")
}
```

- [ ] **Step 4: Run tests, confirm pass**

Run: `cd tools/claude-usage && go test ./... -run TestStatusText -v`
Expected: PASS, all four subtests.

- [ ] **Step 5: Checkpoint**

Run: `cd tools/claude-usage && go vet ./... && go test ./...`
Expected: clean.

---

### Task 3: Config (tier label and port)

The tier label ("Max 20x") is **not** in the statusline payload — reading it would
mean going back to the credentials file, the exact dependency this redesign removes.
So it is set once at install and stored.

**Files:**
- Create: `tools/claude-usage/config.go`
- Test: `tools/claude-usage/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Config struct { Tier string; Port string }`, `func LoadConfig(dir string) Config`, `func SaveConfig(dir string, c Config) error`, `func ConfigDir() string`.

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigMissingReturnsDefaults(t *testing.T) {
	c := LoadConfig(t.TempDir())
	if c.Tier != "" || c.Port != "auto" {
		t.Errorf("defaults = %+v; want Tier:\"\" Port:\"auto\"", c)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	if err := SaveConfig(dir, Config{Tier: "Max 20x", Port: "/dev/ttyUSB0"}); err != nil {
		t.Fatal(err)
	}
	c := LoadConfig(dir)
	if c.Tier != "Max 20x" || c.Port != "/dev/ttyUSB0" {
		t.Errorf("round trip = %+v", c)
	}
}

func TestLoadConfigCorruptFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := LoadConfig(dir) // must not panic or error - fail open
	if c.Port != "auto" {
		t.Errorf("corrupt config should yield defaults, got %+v", c)
	}
}
```

- [ ] **Step 2: Run it, confirm failure**

Run: `cd tools/claude-usage && go test ./... -run TestLoadConfig -v`
Expected: FAIL — `undefined: LoadConfig`

- [ ] **Step 3: Write `config.go`**

```go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	Tier string `json:"tier"` // e.g. "Max 20x"; not in the statusline payload
	Port string `json:"port"` // "auto" or an explicit device path
}

func defaults() Config { return Config{Tier: "", Port: "auto"} }

// ConfigDir is ~/.config/claude-usage (honours XDG_CONFIG_HOME).
func ConfigDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "."
		}
		base = filepath.Join(home, ".config")
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
```

- [ ] **Step 4: Run tests, confirm pass**

Run: `cd tools/claude-usage && go test ./... -run TestLoadConfig -v && go test ./... -run TestSaveThenLoad -v`
Expected: PASS.

- [ ] **Step 5: Checkpoint**

Run: `cd tools/claude-usage && go vet ./... && go test ./...`
Expected: clean.

---

### Task 4: Session and busy inference

The payload has **no busy field**. Infer it from cost movement across invocations,
using only documented data. Each session writes one small file keyed by `session_id`.

**Files:**
- Create: `tools/claude-usage/sessions.go`
- Test: `tools/claude-usage/sessions_test.go`

**Interfaces:**
- Consumes: `Payload` (Task 1).
- Produces: `func RecordSession(dir string, p *Payload, now time.Time) error`, `func SessionLine(dir string, now time.Time) (string, bool)`, constants `sessionActiveWindow`, `busyWindow`, `staleWindow`.

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"testing"
	"time"
)

func payloadWithCost(id string, cost float64) *Payload {
	pl := &Payload{SessionID: id}
	pl.Cost.TotalCostUSD = cost
	return pl
}

func TestSessionLineCountsAndBusy(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Unix(10000, 0)

	// Session A: cost moves -> busy. Session B: silent -> present, not busy.
	if err := RecordSession(dir, payloadWithCost("A", 1.00), t0); err != nil {
		t.Fatal(err)
	}
	if err := RecordSession(dir, payloadWithCost("B", 5.00), t0); err != nil {
		t.Fatal(err)
	}
	if err := RecordSession(dir, payloadWithCost("A", 1.25), t0.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := RecordSession(dir, payloadWithCost("B", 5.00), t0.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}

	got, ok := SessionLine(dir, t0.Add(6*time.Second))
	if !ok || got != "@S|sessions=2|busy=1\n" {
		t.Errorf("SessionLine() = %q,%v; want \"@S|sessions=2|busy=1\\n\",true", got, ok)
	}
}

func TestSessionDropsOutAfterSixtySeconds(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Unix(10000, 0)
	if err := RecordSession(dir, payloadWithCost("A", 1.0), t0); err != nil {
		t.Fatal(err)
	}
	got, ok := SessionLine(dir, t0.Add(90*time.Second))
	if !ok || got != "@S|sessions=0|busy=0\n" {
		t.Errorf("stale session should drop out; got %q,%v", got, ok)
	}
}

func TestBusyDecaysAfterThirtySeconds(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Unix(10000, 0)
	if err := RecordSession(dir, payloadWithCost("A", 1.0), t0); err != nil {
		t.Fatal(err)
	}
	if err := RecordSession(dir, payloadWithCost("A", 2.0), t0.Add(1*time.Second)); err != nil {
		t.Fatal(err)
	}
	// Still within busy window.
	if got, _ := SessionLine(dir, t0.Add(10*time.Second)); got != "@S|sessions=1|busy=1\n" {
		t.Errorf("should be busy at +10s, got %q", got)
	}
	// Cost stopped moving; 40s since last change > 30s busy window.
	if got, _ := SessionLine(dir, t0.Add(41*time.Second)); got != "@S|sessions=1|busy=0\n" {
		t.Errorf("should be idle at +41s, got %q", got)
	}
}

func TestRecordSessionIgnoresEmptyID(t *testing.T) {
	dir := t.TempDir()
	if err := RecordSession(dir, payloadWithCost("", 1.0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if got, _ := SessionLine(dir, time.Unix(2, 0)); got != "@S|sessions=0|busy=0\n" {
		t.Errorf("empty session id must not create state, got %q", got)
	}
}
```

- [ ] **Step 2: Run it, confirm failure**

Run: `cd tools/claude-usage && go test ./... -run TestSession -v`
Expected: FAIL — `undefined: RecordSession`

- [ ] **Step 3: Write `sessions.go`**

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	sessionActiveWindow = 60 * time.Second // seen this recently => counted
	busyWindow          = 30 * time.Second // cost moved this recently => busy
	staleWindow         = 10 * time.Minute // older than this => pruned
)

type sessionState struct {
	LastSeen     int64   `json:"last_seen"`      // unix seconds
	LastCost     float64 `json:"last_cost"`      // total_cost_usd
	LastCostMove int64   `json:"last_cost_move"` // unix seconds cost last changed
}

// sessionFile keys state by session_id, hex-encoded so an id can never contain
// a path separator or escape the state dir.
func sessionFile(dir, id string) string {
	return filepath.Join(dir, fmt.Sprintf("%x.json", id))
}

// RecordSession writes this invocation's state. Requires refreshInterval so idle
// sessions keep reporting and do not fall out of the count.
func RecordSession(dir string, p *Payload, now time.Time) error {
	if p == nil || p.SessionID == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := sessionFile(dir, p.SessionID)

	st := sessionState{LastCostMove: 0}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.LastCost != p.Cost.TotalCostUSD {
		st.LastCostMove = now.Unix()
	}
	st.LastCost = p.Cost.TotalCostUSD
	st.LastSeen = now.Unix()

	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// SessionLine renders @S from the state dir, pruning stale files as it goes.
func SessionLine(dir string, now time.Time) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "@S|sessions=0|busy=0\n", true
	}
	total, busy := 0, 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var st sessionState
		if json.Unmarshal(b, &st) != nil {
			continue
		}
		age := now.Sub(time.Unix(st.LastSeen, 0))
		if age > staleWindow {
			_ = os.Remove(path)
			continue
		}
		if age > sessionActiveWindow {
			continue
		}
		total++
		if st.LastCostMove > 0 && now.Sub(time.Unix(st.LastCostMove, 0)) <= busyWindow {
			busy++
		}
	}
	return fmt.Sprintf("@S|sessions=%d|busy=%d\n", total, busy), true
}
```

- [ ] **Step 4: Run tests, confirm pass**

Run: `cd tools/claude-usage && go test ./... -run TestSession -v && go test ./... -run TestBusy -v && go test ./... -run TestRecord -v`
Expected: PASS.

- [ ] **Step 5: Checkpoint**

Run: `cd tools/claude-usage && go vet ./... && go test ./...`
Expected: clean.

---

### Task 5: The holder

Owns the serial port so the board resets at most once per holder lifetime instead
of once per statusline invocation. This is the whole reason the design has a
background process at all.

**Files:**
- Create: `tools/claude-usage/holder.go`
- Create: `tools/claude-usage/lock_unix.go`
- Test: `tools/claude-usage/holder_test.go`
- Test: `tools/claude-usage/lock_unix_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Sink interface { Write([]byte) (int, error); Close() error }`, `func Serve(sock string, sink Sink, idle time.Duration) error`, `func SocketPath() string`, `func LockPath() string`, `func TryLock(path string) (release func(), ok bool, err error)`.

- [ ] **Step 1: Write the failing test**

The port is faked so this runs without hardware. `Serve` takes a `Sink` precisely
so the real serial port can be swapped for a buffer.

```go
package main

import (
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeSink struct {
	mu     sync.Mutex
	got    []byte
	closed bool
}

func (f *fakeSink) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, b...)
	return len(b), nil
}
func (f *fakeSink) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}
func (f *fakeSink) String() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.got)
}

// Closed is mutex-guarded: Serve closes the sink from another goroutine, so a
// bare field read here would trip the race detector.
func (f *fakeSink) Closed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func TestServeWritesLinesThroughToSink(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	sink := &fakeSink{}
	done := make(chan error, 1)
	go func() { done <- Serve(sock, sink, 2*time.Second) }()

	waitForSocket(t, sock)
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("@U|five=24\n@S|sessions=1|busy=0\n")); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sink.String() == "@U|five=24\n@S|sessions=1|busy=0\n" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("sink got %q", sink.String())
}

func TestServeExitsWhenIdleAndClosesPort(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	sink := &fakeSink{}
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- Serve(sock, sink, 300*time.Millisecond) }()

	select {
	case <-done:
		if time.Since(start) < 250*time.Millisecond {
			t.Fatal("exited too early")
		}
		if !sink.Closed() {
			t.Fatal("idle exit must close the port so `make usage` can flash")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("holder never exited on idle")
	}
}

func TestSecondHolderFailsToBind(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	go func() { _ = Serve(sock, &fakeSink{}, 2*time.Second) }()
	waitForSocket(t, sock)
	if err := Serve(sock, &fakeSink{}, time.Second); err == nil {
		t.Fatal("second holder should fail to bind, not steal the port")
	}
}

func waitForSocket(t *testing.T, sock string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.Dial("unix", sock); err == nil {
			c.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("socket never appeared")
}
```

- [ ] **Step 2: Run it, confirm failure**

Run: `cd tools/claude-usage && go test ./... -run TestServe -v`
Expected: FAIL — `undefined: Serve`

- [ ] **Step 3: Write `holder.go`**

```go
package main

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Sink is where accepted lines go. The real one is the serial port; tests use a
// buffer, which is why Serve does not open the port itself.
type Sink interface {
	Write([]byte) (int, error)
	Close() error
}

func runtimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return os.TempDir() // macOS has no XDG_RUNTIME_DIR
}

// SocketPath prefers XDG_RUNTIME_DIR (Linux), falls back to TMPDIR (macOS).
func SocketPath() string { return filepath.Join(runtimeDir(), "claude-usage.sock") }

// LockPath is the sidecar flock file guarding single-holder access to the port.
func LockPath() string { return filepath.Join(runtimeDir(), "claude-usage.lock") }

// Serve owns sink for its lifetime and writes every received line to it.
// Returns after `idle` elapses with no line, closing sink so the port is
// released for flashing. A second holder fails to bind and must not steal
// the port.
func Serve(sock string, sink Sink, idle time.Duration) error {
	// A stale socket file from a killed holder would block bind; remove it only
	// if nothing is listening.
	if c, err := net.Dial("unix", sock); err == nil {
		c.Close()
		return &net.OpError{Op: "listen", Net: "unix", Err: os.ErrExist}
	}
	_ = os.Remove(sock)

	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	defer ln.Close()
	defer os.Remove(sock)
	defer sink.Close()

	var mu sync.Mutex
	last := time.Now()
	touch := func() { mu.Lock(); last = time.Now(); mu.Unlock() }

	go func() {
		for {
			time.Sleep(50 * time.Millisecond)
			mu.Lock()
			quiet := time.Since(last)
			mu.Unlock()
			if quiet >= idle {
				ln.Close() // unblocks Accept -> Serve returns -> sink.Close()
				return
			}
		}
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			return nil // listener closed by the idle watchdog
		}
		touch()
		go func(c net.Conn) {
			defer c.Close()
			sc := bufio.NewScanner(c)
			for sc.Scan() {
				line := append(sc.Bytes(), '\n')
				mu.Lock()
				_, _ = sink.Write(line) // a dead board must not kill the holder
				mu.Unlock()
				touch()
			}
		}(conn)
	}
}
```

- [ ] **Step 4: Write `lock_unix.go` — the authoritative single-instance guard**

The socket bind alone is NOT sufficient. Two holders can race: both dial (fail),
both `os.Remove` the socket, and the second unlinks the first's socket and binds
its own. Linux allows multiple opens of `/dev/ttyUSB0` (no `TIOCEXCL` by default),
so both would then drive the board with interleaved writes. `flock` closes that
race before the port is ever opened.

```go
//go:build unix

package main

import (
	"os"
	"syscall"
)

// TryLock takes an exclusive non-blocking flock. ok=false means another holder
// already owns the port - the caller must exit, not proceed. The returned
// release closes the fd (dropping the lock); the lockfile itself is left behind
// deliberately, since an flock on a stale file is harmless.
func TryLock(path string) (release func(), ok bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, false, nil // held by a live holder
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, true, nil
}
```

- [ ] **Step 5: Write the failing lock test**

```go
//go:build unix

package main

import (
	"path/filepath"
	"testing"
)

func TestTryLockExcludesSecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	release, ok, err := TryLock(path)
	if err != nil || !ok {
		t.Fatalf("first TryLock = %v,%v; want ok", ok, err)
	}
	if _, ok2, _ := TryLock(path); ok2 {
		t.Fatal("second TryLock succeeded - two holders would both drive the board")
	}
	release()
	release2, ok3, err := TryLock(path)
	if err != nil || !ok3 {
		t.Fatalf("TryLock after release = %v,%v; want ok", ok3, err)
	}
	release2()
}
```

- [ ] **Step 6: Run tests, confirm pass**

Run: `cd tools/claude-usage && go test ./... -run "TestServe|TestSecondHolder|TestTryLock" -v`
Expected: PASS, four tests.

- [ ] **Step 7: Checkpoint**

Run: `cd tools/claude-usage && go vet ./... && go test ./... -race`
Expected: clean, no race warnings (`Serve` is concurrent by design).

---

### Task 6: Client and detached spawn

**Files:**
- Create: `tools/claude-usage/client.go`
- Create: `tools/claude-usage/spawn_unix.go`
- Test: `tools/claude-usage/client_test.go`

**Interfaces:**
- Consumes: `SocketPath` (Task 5).
- Produces: `func Send(sock string, line string, timeout time.Duration) error`, `func SpawnHolder(exe string) error`.

Verified: a `setsid`'d child survives `SIGKILL` of its spawner's whole process
group (`tmp/spawn_spike2.sh` — spawner confirmed dead, child kept running).

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestSendDeliversLine(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	sink := &fakeSink{}
	go func() { _ = Serve(sock, sink, 2*time.Second) }()
	waitForSocket(t, sock)

	if err := Send(sock, "@U|five=24\n", time.Second); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if sink.String() == "@U|five=24\n" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("sink got %q", sink.String())
}

func TestSendErrorsWhenNoHolder(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "absent.sock")
	if err := Send(sock, "@U|five=1\n", 200*time.Millisecond); err == nil {
		t.Fatal("Send to a missing holder must error so the caller can spawn one")
	}
}

func TestSendDoesNotBlockForeverOnDeadListener(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock) // listener that never accepts
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() { _ = Send(sock, "@U|five=1\n", 200*time.Millisecond); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Send must respect its timeout - it runs inside the statusline")
	}
}
```

- [ ] **Step 2: Run it, confirm failure**

Run: `cd tools/claude-usage && go test ./... -run TestSend -v`
Expected: FAIL — `undefined: Send`

- [ ] **Step 3: Write `client.go`**

```go
package main

import (
	"net"
	"time"
)

// Send delivers one line to the holder. The timeout is a hard ceiling: this runs
// inside the user's statusline and must never stall Claude Code.
func Send(sock string, line string, timeout time.Duration) error {
	conn, err := net.DialTimeout("unix", sock, timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	_, err = conn.Write([]byte(line))
	return err
}
```

- [ ] **Step 4: Write `spawn_unix.go`**

```go
//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// SpawnHolder starts `exe --hold` in its own session so it survives this process
// exiting - and survives Claude Code killing the statusline's process group.
// Setsid is what buys that: measured in the spike, a setsid'd child kept running
// after its spawner was confirmed SIGKILLed by process group.
func SpawnHolder(exe string) error {
	cmd := exec.Command(exe, "--hold")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	// Deliberately no Wait: let it be orphaned and reparented to init.
	return cmd.Process.Release()
}
```

- [ ] **Step 5: Run tests, confirm pass**

Run: `cd tools/claude-usage && go test ./... -run TestSend -v`
Expected: PASS, three tests.

- [ ] **Step 6: Checkpoint**

Run: `cd tools/claude-usage && go vet ./... && go test ./... -race`
Expected: clean.

---

### Task 7: Serial port discovery and the real sink

**Files:**
- Create: `tools/claude-usage/port_unix.go`
- Test: `tools/claude-usage/port_unix_test.go`

**Interfaces:**
- Consumes: `Config` (Task 3), `Sink` (Task 5).
- Produces: `func FindPort(cfg Config) (string, error)`, `func OpenSink(dev string) (Sink, error)`.

- [ ] **Step 1: Add the dependency**

```bash
cd tools/claude-usage && go get go.bug.st/serial@latest
```

- [ ] **Step 2: Write the failing test**

`FindPort` is testable; `OpenSink` needs hardware and is covered by the manual
verification in Task 9.

```go
//go:build unix

package main

import "testing"

func TestFindPortHonoursExplicitConfig(t *testing.T) {
	got, err := FindPort(Config{Port: "/dev/ttyFAKE"})
	if err != nil || got != "/dev/ttyFAKE" {
		t.Errorf("explicit port = %q,%v; want /dev/ttyFAKE,nil", got, err)
	}
}

func TestFindPortAutoErrorsWhenNothingPresent(t *testing.T) {
	// candidateGlobs is overridable so auto-detect is testable without hardware.
	old := candidateGlobs
	candidateGlobs = []string{"/nonexistent/ttyUSB*"}
	defer func() { candidateGlobs = old }()

	if _, err := FindPort(Config{Port: "auto"}); err == nil {
		t.Fatal("auto with no devices must error, not return empty string")
	}
}
```

- [ ] **Step 3: Run it, confirm failure**

Run: `cd tools/claude-usage && go test ./... -run TestFindPort -v`
Expected: FAIL — `undefined: FindPort`

- [ ] **Step 4: Write `port_unix.go`**

```go
//go:build unix

package main

import (
	"errors"
	"path/filepath"
	"sort"

	"go.bug.st/serial"
)

// candidateGlobs is a var, not a const, so tests can point it at nothing.
// Covers the CP2102 on Linux (ttyUSB*) and macOS (cu.SLAB_*, cu.usbserial-*).
var candidateGlobs = []string{
	"/dev/ttyUSB*",
	"/dev/cu.SLAB_USBtoUART*",
	"/dev/cu.usbserial-*",
}

var errNoPort = errors.New("no serial port found")

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

// OpenSink opens the port with DTR and RTS DEASSERTED. Asserting them drives the
// ESP8266 into its UART bootloader (docs/HARDWARE.md). The open still resets the
// board once - unavoidable, measured - which is exactly why the holder keeps it
// open instead of reopening per line.
func OpenSink(dev string) (Sink, error) {
	port, err := serial.Open(dev, &serial.Mode{
		BaudRate: 115200,
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
```

- [ ] **Step 5: Run tests, confirm pass**

Run: `cd tools/claude-usage && go test ./... -run TestFindPort -v`
Expected: PASS, two tests.

- [ ] **Step 6: Checkpoint**

Run: `cd tools/claude-usage && go vet ./... && go test ./...`
Expected: clean.

---

### Task 8: `main.go` and `--install`

**Files:**
- Create: `tools/claude-usage/main.go`
- Create: `tools/claude-usage/install.go`
- Test: `tools/claude-usage/install_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: the `claude-usage` binary; `func InstallStatusLine(settingsPath, exe string) error`.

- [ ] **Step 1: Write the failing test**

`--install` must merge into `settings.json`, never clobber it.

```go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallPreservesExistingSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"theme":"dark","env":{"A":"1"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallStatusLine(path, "/opt/claude-usage"); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["theme"] != "dark" {
		t.Errorf("install clobbered unrelated settings: %v", got)
	}
	sl, ok := got["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("no statusLine written: %v", got)
	}
	if sl["type"] != "command" || sl["command"] != "/opt/claude-usage" {
		t.Errorf("bad statusLine: %v", sl)
	}
	if sl["refreshInterval"] != float64(10) {
		t.Errorf("refreshInterval must be 10 so idle sessions keep reporting, got %v", sl["refreshInterval"])
	}
}

func TestInstallCreatesFileWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := InstallStatusLine(path, "/opt/claude-usage"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("settings.json should have been created")
	}
}
```

- [ ] **Step 2: Run it, confirm failure**

Run: `cd tools/claude-usage && go test ./... -run TestInstall -v`
Expected: FAIL — `undefined: InstallStatusLine`

- [ ] **Step 3: Write `install.go`**

```go
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
```

- [ ] **Step 4: Write `main.go`**

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const version = "1.0.0"

func stateDir() string { return filepath.Join(ConfigDir(), "sessions") }

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--hold":
			runHolder()
			return
		case "--install":
			runInstall()
			return
		case "--version":
			fmt.Println("claude-usage", version)
			return
		}
	}
	runStatusline()
}

// runStatusline is the hot path. It must ALWAYS print a line and exit 0.
func runStatusline() {
	var p *Payload
	if b, err := io.ReadAll(os.Stdin); err == nil && len(b) > 0 {
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

	var lines []string
	if u, ok := UsageLine(p, cfg.Tier, now); ok {
		lines = append(lines, u)
	}
	if s, ok := SessionLine(stateDir(), now); ok {
		lines = append(lines, s)
	}
	if len(lines) == 0 {
		return
	}

	sock := SocketPath()
	for _, l := range lines {
		if err := Send(sock, l, 100*time.Millisecond); err != nil {
			// No holder: spawn one and drop this line. The next invocation
			// lands normally - one skipped line is invisible at this cadence.
			if exe, e := os.Executable(); e == nil {
				_ = SpawnHolder(exe)
			}
			return
		}
	}
}

func runHolder() {
	// Take the lock BEFORE opening the port. Linux permits multiple opens of
	// /dev/ttyUSB0 (no TIOCEXCL), so without this two holders would both drive
	// the board with interleaved writes. Use `return`, never os.Exit: os.Exit
	// skips defers and would leak the lock.
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
	sink, err := OpenSink(dev)
	if err != nil {
		return
	}
	_ = Serve(SocketPath(), sink, 5*time.Minute)
}

func runInstall() {
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
	if cfg.Tier == "" {
		cfg.Tier = "Max 20x" // not in the payload; edit config.json to change
	}
	if err := SaveConfig(ConfigDir(), cfg); err != nil {
		fmt.Fprintln(os.Stderr, "config save failed:", err)
		os.Exit(1)
	}
	fmt.Println("installed statusLine ->", exe)
	fmt.Println("tier:", cfg.Tier, "(edit", filepath.Join(ConfigDir(), "config.json"), "to change)")
}
```

- [ ] **Step 5: Run tests and build**

Run: `cd tools/claude-usage && go test ./... && go build -o claude-usage . && ./claude-usage --version`
Expected: `ok`, then `claude-usage 1.0.0`.

- [ ] **Step 6: Verify fail-open by hand**

Run: `cd tools/claude-usage && echo '' | ./claude-usage; echo "exit=$?"`
Expected: prints `usage —`, then `exit=0`.

Run: `cd tools/claude-usage && echo '{not json' | ./claude-usage; echo "exit=$?"`
Expected: prints `usage —`, then `exit=0`.

Run: `cd tools/claude-usage && echo '{"session_id":"x","rate_limits":{"five_hour":{"used_percentage":23.5,"resets_at":99999999999}}}' | ./claude-usage; echo "exit=$?"`
Expected: prints `5h 24%`, then `exit=0`.

- [ ] **Step 7: Verify cross-compilation still works**

The core must stay platform-neutral. Windows files do not exist yet, so this is
expected to fail on `FindPort`/`SpawnHolder` — that failure is the proof the
Windows gap is real and scoped, not silently broken.

Run: `cd tools/claude-usage && GOOS=darwin GOARCH=arm64 go build -o /dev/null .`
Expected: SUCCESS — macOS is in scope and shares the `unix` build tag.

- [ ] **Step 8: Checkpoint**

Run: `cd tools/claude-usage && go vet ./... && go test ./... -race`
Expected: clean.

---

### Task 9: Firmware — count the reset down on-board

The host now sends **seconds remaining** instead of a baked `"52m"` string, so the
panel stays correct with nothing running. This slots into the existing dirty-region
path: `drawBar` already repaints when the formatted string changes.

**Files:**
- Modify: `firmware/usage_display/usage_display.ino:26-35` (the `Usage` struct)
- Modify: `firmware/usage_display/usage_display.ino:271-272` (key parsing)
- Modify: `firmware/usage_display/usage_display.ino` (add `formatRemaining`, `tickResets`; call from `loop()`)

**Interfaces:**
- Consumes: the `@U` line from Task 1 (`five_s`, `seven_s`).
- Produces: nothing for other tasks.

- [ ] **Step 1: Extend the `Usage` struct**

Replace lines 26-35 with:

```cpp
struct Usage {
  bool     valid       = false;   // have we ever received a line?
  bool     authError   = false;   // retained: agent reported a rejected token
  uint8_t  fivePct     = 0;
  uint8_t  sevenPct    = 0;
  char     sub[13]     = "";      // subscription tier, e.g. "Max 20x"
  char     fiveReset[12] = "";    // rendered "52m", "3d 4h" - derived, see tickResets
  char     sevenReset[12] = "";
  uint32_t fiveResetSec  = 0;     // seconds to reset at the moment of receipt
  uint32_t sevenResetSec = 0;
  uint32_t resetBaseMs   = 0;     // millis() when the above were received
  uint32_t updatedMs   = 0;       // millis() of the last good line
};
```

- [ ] **Step 2: Parse the new keys**

Replace lines 271-272 (the `fivereset`/`sevenreset` cases) with:

```cpp
    else if (!strcmp(key, "five_s"))  { next.fiveResetSec  = (uint32_t)strtoul(val, nullptr, 10);
                                        next.resetBaseMs   = millis(); }
    else if (!strcmp(key, "seven_s")) { next.sevenResetSec = (uint32_t)strtoul(val, nullptr, 10);
                                        next.resetBaseMs   = millis(); }
```

- [ ] **Step 3: Add `formatRemaining` and `tickResets` above `renderUsage`**

`.ino` auto-prototyping hoists prototypes above the file body, so these must be
defined before use — the `BarGeom`/`PixelFn` trap in CLAUDE.md.

```cpp
/* "3d 4h" / "1h 43m" / "52m" — matches the strings the agent used to send. */
static void formatRemaining(uint32_t sec, char *out, size_t n) {
  if (sec >= 86400UL)      snprintf(out, n, "%lud %luh", sec / 86400UL, (sec % 86400UL) / 3600UL);
  else if (sec >= 3600UL)  snprintf(out, n, "%luh %lum", sec / 3600UL, (sec % 3600UL) / 60UL);
  else                     snprintf(out, n, "%lum", sec / 60UL);
}

/* Recompute the reset strings from the last received seconds + elapsed millis.
   Dirties a meter only when the *rendered* text changes (~once a minute), so
   this costs nothing per frame. Unsigned subtraction wraps correctly at the
   49-day millis() rollover. */
static void tickResets() {
  if (!usage.valid) return;
  uint32_t elapsed = (millis() - usage.resetBaseMs) / 1000UL;
  char buf[12];

  uint32_t f = usage.fiveResetSec > elapsed ? usage.fiveResetSec - elapsed : 0;
  formatRemaining(f, buf, sizeof buf);
  if (strcmp(buf, usage.fiveReset)) {
    strlcpy(usage.fiveReset, buf, sizeof usage.fiveReset);
    usageDirty |= UD_VAL5;
  }

  uint32_t s = usage.sevenResetSec > elapsed ? usage.sevenResetSec - elapsed : 0;
  formatRemaining(s, buf, sizeof buf);
  if (strcmp(buf, usage.sevenReset)) {
    strlcpy(usage.sevenReset, buf, sizeof usage.sevenReset);
    usageDirty |= UD_VAL7;
  }
}
```

- [ ] **Step 4: Fix the dirty comparison in `parseUsageLine`**

Lines 285-290 compare `next.fiveReset` to `usage.fiveReset`, but those strings are
now derived by `tickResets`, not received. Compare the seconds instead. Replace with:

```cpp
  if (!usage.valid || next.fivePct != usage.fivePct ||
      next.fiveResetSec != usage.fiveResetSec)
    dirty |= UD_VAL5;
  if (!usage.valid || next.sevenPct != usage.sevenPct ||
      next.sevenResetSec != usage.sevenResetSec)
    dirty |= UD_VAL7;
```

- [ ] **Step 5: Call `tickResets()` from `loop()`**

Add it immediately after `pumpSerial();` in `loop()`, before the render call, so a
changed string is picked up in the same frame.

- [ ] **Step 6: Build**

Run: `cd firmware && make build-all`
Expected: compiles clean, no "does not name a type".

- [ ] **Step 7: Flash and verify against real hardware**

The agent still owns the port at this point, and only one process may:

```bash
systemctl --user stop claude-usage
cd firmware && make usage
```

- [ ] **Step 8: Drive it by hand and watch the countdown**

```bash
cd tools/claude-usage && go build -o claude-usage . && ./claude-usage --hold &
sleep 2
printf '@U|five=24|seven=41|sub=Max 20x|five_s=3600|seven_s=280800\n' \
  | nc -U "${XDG_RUNTIME_DIR:-/tmp}/claude-usage.sock"
```

Expected on the panel: `5-HOUR 24%` with `60m`, `WEEKLY 41%` with `3d 6h`.
Wait ~60 s: the 5-hour reset must tick to `59m` **without another line being sent**.
That tick is the whole point of this task — it proves the panel no longer lies when
nothing is running.

- [ ] **Step 9: Checkpoint**

Confirm with your own eyes. `docs/HARDWARE.md`: the SPI bus is write-only, so no
automated check can confirm the picture. If the countdown does not advance,
`tickResets()` is not being called from `loop()`.

---

### Task 10: Retire the Python agent and update the docs

Only after Task 9 is confirmed working on hardware. This deletes the fallback.

**Files:**
- Delete: `agent/claude_usage_agent.py`, `agent/install.sh`, `agent/requirements.txt`, `agent/claude-usage.service`, `agent/README.md`
- Create: `firmware/scripts/reset_probe.py`
- Modify: `README.md`, `CLAUDE.md`, `tasks/todo.md`

- [ ] **Step 1: Uninstall the running service**

```bash
systemctl --user disable --now claude-usage
rm -f "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/claude-usage.service"
systemctl --user daemon-reload
```

Verify: `systemctl --user status claude-usage` → `Unit claude-usage.service could not be found.`

- [ ] **Step 2: Confirm the reset probe is preserved**

`firmware/scripts/reset_probe.py` was already copied into the repo when this plan
was written (it originated in a scratch dir that is deleted with the job). It proves
every port open resets the board, and would catch a regression where the holder
reopens per line instead of holding.

It imports `serial`, which lived in the agent's venv that Step 3 deletes. Verify it
still runs, and if pyserial is gone, note in its docstring that it needs
`pip install pyserial` (it is a manual hardware probe, not part of `go test`):

Run: `python3 -c "import serial; print('pyserial', serial.__version__)"`
Expected: a version, or an ImportError that you record in the docstring.

- [ ] **Step 3: Delete the agent**

```bash
rm -rf agent/
```

- [ ] **Step 4: Update `CLAUDE.md`**

Rewrite the "One transport (USB serial)" section: the agent no longer reads the
OAuth credentials file or polls `GET /api/oauth/usage`. Data now arrives from Claude
Code's documented statusline JSON via `tools/claude-usage`. Update the `@U` wire
format to `five_s`/`seven_s` (seconds remaining, counted down on-board). Replace the
`agent/` command block with:

```bash
cd tools/claude-usage && go build -o claude-usage . && ./claude-usage --install
```

Add to the gotchas: **every** serial open resets the board (measured; `-hupcl` does
not help), which is why a holder process owns the port. Amend the "Only one process
owns /dev/ttyUSB0" gotcha: the holder now exits after 5 minutes idle, so flashing
usually just works.

- [ ] **Step 5: Update `README.md`**

Replace agent references with the statusline install. State the accepted limitation:
usage from claude.ai in a browser reaches the board only if a Claude Code session is
open somewhere to report it.

- [ ] **Step 6: Add a review section to `tasks/todo.md`**

Per `CLAUDE.md` task-management rules. Record what shipped, the two measured facts
(reset-on-every-open; setsid survives group kill), and that the ToS exposure and the
401/429 bug class are gone with the agent.

- [ ] **Step 7: Final verification**

```bash
cd tools/claude-usage && go vet ./... && go test ./... -race
cd ../../firmware && make build-all
grep -rn "claude_usage_agent\|/api/oauth/usage\|fivereset" --include="*.md" --include="*.ino" . || echo "no stale references"
```

Expected: tests pass, firmware builds, no stale references.

- [ ] **Step 8: Confirm end-to-end in a real session**

Open a fresh Claude Code session. Expected: usage appears in your terminal
statusline, and within ~10 s the panel shows the same numbers. Confirm by eye.

---

## Deferred: Windows

Out of scope for this plan, per the spec. Requires, in order:

1. A spike proving a statusline command can spawn a surviving detached child on
   Windows (`CREATE_NEW_PROCESS_GROUP|DETACHED_PROCESS` via `syscall.SysProcAttr`).
   The `setsid` result does **not** transfer — POSIX reasoning does not apply.
2. `spawn_windows.go`, `port_windows.go` (COM enumeration via
   `HKLM\HARDWARE\DEVICEMAP\SERIALCOMM`), named pipe in place of the Unix socket.
3. A Windows `--install` path (`%USERPROFILE%\.claude\settings.json`).

If the spike fails, the fallback is a normal Task Scheduler entry for the holder,
which reintroduces a per-OS install on Windows only.
