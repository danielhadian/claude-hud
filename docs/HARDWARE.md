# Hardware reference — ST7735 1.8" on NodeMcu (ESP8266)

This documents the **reference build** — the specific board and panel the
shipped firmware was measured on. claude-hud itself is hardware-agnostic:
other panels/boards need only `firmware/lib/Panel/src/Panel.h` edits or a
fresh implementation of [PROTOCOL.md](PROTOCOL.md). The traps below are
still worth reading whatever you build; several generalise.

Every number here was **measured on this board**, not copied from a datasheet.
Re-run `make probe` to regenerate them.

---

## 1. Wiring

`MOSI` and `SCLK` are fixed by the ESP8266's HSPI peripheral and **cannot be
remapped**. Everything else is a free choice; these are the ones the code uses.

| Display pin | NodeMcu | GPIO | Notes |
|---|---|---|---|
| `1-RST` | `D4` | 2 | boot strap: must be HIGH at boot |
| `2-CS` | `D8` | 15 | boot strap: must be LOW at boot |
| `3-D/C` | `D3` | 0 | boot strap: must be HIGH at boot |
| `4-DIN` (MOSI) | `D7` | 13 | **fixed** — hardware SPI |
| `5-CLK` (SCLK) | `D5` | 14 | **fixed** — hardware SPI |
| `6-VCC` | **`Vin`** | — | **5 V — see below. Not `3V3`.** |
| `7-BL` | `3V3` | — | drives transistor `Q1`; 3.3 V logic is correct |
| `8-GND` | `GND` | — | |

### ⚠️ VCC must be 5 V, not 3.3 V

The blue ST7735+SD module has **its own AMS1117-3.3 regulator** (`U1`) and ships
with solder jumper **`J1` open**, which means the `VCC` pin feeds that regulator.
Its silkscreen says so:

```
Vin
VCC = 5V   J1 OPEN      <-- factory default
VCC = 3V   J1 SHORT
```

An AMS1117 drops **~1.1 V**, so feeding it 3.3 V yields only **~2.2 V** on the
module's internal rail — below the forward voltage the backlight LEDs need.

**Symptom:** the panel flashes on at power-up, then goes dark and stays dark.
It looks exactly like a dead display or a broken data wire. It is neither.

*(If you'd rather run from 3.3 V, bridge `J1` with solder to bypass the
regulator, then `VCC → 3V3` is correct.)*

### Boot-strapping pins

All three control pins (`RST`/`CS`/`DC` → GPIO 2/15/0) are ESP8266 strapping
pins. This is fine and is the canonical ST7735 wiring: the display's CS/DC/RST
are high-impedance CMOS inputs and don't fight the NodeMcu's strapping
resistors. It only matters if the board ever refuses to boot with the display
attached.

---

## 2. The panel

| Property | Value |
|---|---|
| Controller | ST7735, `INITR_BLACKTAB` |
| Resolution | **128 × 160** (rotation 0/2) · 160 × 128 (rotation 1/3) |
| Pixels | 20,480 |
| Colour | **RGB565**, 16 bpp — 5 red, 6 green, 5 blue |
| Byte order on the wire | **big-endian** (MSB first) |
| Offsets (colstart/rowstart) | 0, 0 |
| Full framebuffer | **40,960 bytes** |

**Wire format matters.** The ST7735 wants each pixel as `hi, lo`. If you store
bitmaps that way, rows stream straight from flash to SPI with zero per-pixel
work. `firmware/scripts/svg2rgb565.py` emits exactly this layout.

---

## 3. Memory — the constraint that shapes everything

| | bytes |
|---|---|
| Free heap at boot | **50,080** |
| Largest single `malloc` | 50,076 |
| Full 128×160 framebuffer | 40,960 |
| Left over if you allocate one | **~9,000** |

**Do not keep a full framebuffer.** It fits, and then you have ~9 KB for the
network stack, TLS, and your own state — that is not a budget you can build on,
and it will fail later under memory pressure rather than now.

**Render in bands instead.** A `128 × 16` band is **4,096 bytes**. `Panel` has a
streaming API for exactly this:

```cpp
uint8_t band[128 * 16 * 2];
panel.beginBand(0, 0, 128, 160);
for (int y = 0; y < 160; y += 16) {
    renderRows(band, y);          // your code fills the band
    panel.pushBand(band, sizeof band);
}
panel.endBand();
```

Nothing large is ever resident, and it runs at full SPI speed.

Flash is roomy by comparison: **4 MB total, ~1.8 MB free** for sketch + assets.
Put bitmaps in `PROGMEM` and blit them with `panel.blitPROGMEM()`.

---

## 4. SPI clock — measured

The ESP8266 divides 80 MHz by an integer, so **only these rates exist**:
40.0, 26.7, 20.0, 16.0, 13.3, 11.4, 10.0, 8.9, 8.0 MHz. Asking for 27 MHz gets
you 26.7.

Full-screen fills, bulk FIFO writes, 20 frames each:

| Clock | MB/s | fps | ms/frame | efficiency |
|---|---|---|---|---|
| 8.0 MHz | 0.93 | 22.7 | 44.0 | 93.1% |
| 10.0 MHz | 1.14 | 27.9 | 35.8 | 91.4% |
| 13.3 MHz | 1.29 | 31.4 | 31.8 | 77.2% |
| 16.0 MHz | 1.75 | 42.6 | 23.5 | 87.3% |
| 20.0 MHz | 2.12 | 51.6 | 19.4 | 84.6% |
| **26.7 MHz** ← default | **2.67** | **65.1** | **15.4** | **80.0%** |
| 40.0 MHz | 3.66 | 89.3 | 11.2 | 73.2% |

**Efficiency falls as the clock rises. That is expected, not a fault** — the
per-frame setup cost is constant, so it's a larger fraction of a shorter frame.
A healthy bus sits at 75–95%. A wrong clock divider would halve it to ~50%.

**Default is 26.7 MHz** because it's proven stable on dupont jumpers. 40 MHz
measured fine here and gives 89 fps; before trusting it, run the self-test and
look at the **1px checkerboard** — it should be a clean uniform grey. Sparkle,
smear or a shimmering moiré means the clock is too high for your wiring.

---

## 5. Two library bugs you must know about

### 5a. `INITR_REDTAB` is broken — never use it

`Adafruit_ST7735.h` (v1.11.0, **and still on upstream master**) has a constant
collision:

```c
#define INITR_REDTAB      0x01
#define INITR_144GREENTAB 0x01     // should be 0x03
```

`initR()` tests `144GREENTAB` first, so asking for `REDTAB` silently runs the
**1.44" init sequence** and forces the panel to **128×128** — you lose the
bottom 32 rows. Verified here: `initR(INITR_REDTAB)` → *"panel reports 128 x 128 px"*.

Nothing is lost by avoiding it: REDTAB differs from BLACKTAB only in the colour
filter (BGR vs RGB), and the offsets are identical.

Related: `initR()` does not fully reset state between calls on the same object
(`GREENTAB` sets `_colstart=2`, and the RED/BLACK branch never clears it), so
re-initing a live object with a different tab inherits the previous tab's
offsets. `Panel::begin()` sidesteps all of this by always using `BLACKTAB`.

### 5b. Adafruit's ESP8266 pixel path is ~6× slower than the hardware

`Adafruit_SPITFT` has a bulk `writeBytes()` fast path for ESP32, nRF52, RP2040
and SAMD-DMA. **On ESP8266 it falls through to a generic loop** that issues one
library call *per pixel*:

```cpp
while (len--) SPI_WRITE16(*colors++);
```

That is call-overhead bound, not clock bound, so it plateaus near **13% of the
SPI ceiling no matter what clock you set** (measured: 0.44 MB/s of a 3.33 MB/s
ceiling — 10.7 fps).

`Panel` overrides `fillScreen`, `fillRect`, `drawFastHLine` and `drawFastVLine`
to push pixels through the ESP8266's **64-byte SPI hardware FIFO** via
`SPI.writeBytes()`. Per-call overhead drops ~32×, and the bus reaches **80% of
its ceiling** (2.67 MB/s, 65 fps). Ordinary Adafruit_GFX code gets this for free.

Measured effect:

| Operation | Adafruit path | `Panel` |
|---|---|---|
| `fillScreen` | 93.2 ms | **15.3 ms** |
| Full colour-bar screen | 211.9 ms | **33.2 ms** |
| Rotation redraw | 107.1 ms | **27.9 ms** |

---

## 6. The thing firmware can never tell you

**The bus is write-only.** There is no MISO wire (the module's 8-pin TFT header
doesn't even break it out). The ST7735 therefore **cannot be read back** — no
chip-ID read, no register read, no GRAM read.

**Consequence: every software test passes identically with the display
unplugged.** Throughput, clock, "link stable" — all of it measures the
*ESP8266's* SPI peripheral clocking bytes into open air. None of it proves a
single pixel lit up.

Anything claiming to "verify the display" by reading the controller is impossible
with this wiring. The panel is confirmed by **looking at it**, which is why the
self-test draws patterns whose failure modes are visually distinct.

---

## 7. Fault → symptom

| What you see | What it means |
|---|---|
| Panel flashes on at power-up, then dark | **`VCC` on `3V3`.** Move it to `Vin`. (§1) |
| Totally dark, no backlight glow ever | `VCC`/`BL`/`GND` not connected; reseat — those sockets are loose |
| Backlight on, screen black | Data side: `MOSI`/`CLK`/`CS`/`DC`. Serial will still say PASS |
| Bar labelled `RED` renders blue | Panel is BGR; flip the MADCTL colour-filter bit |
| Photo-negative colours | Inversion — call `invertDisplay(true)` |
| Image shifted / coloured band at an edge | Wrong offsets — wrong tab variant |
| Bottom 32 rows missing | You used `INITR_REDTAB`. See §5a |
| Sparkle / smear on the checkerboard | SPI clock too high for the wiring. Drop to 20 or 16 MHz |
| Board won't boot with display attached | A strapping pin is being held. See §1 |
| Random resets under load | Brownout. Keep WiFi off, or add a cap across VCC/GND |
| `bus efficiency ~13%` | You bypassed `Panel` and hit Adafruit's per-pixel path. §5b |

---

## 8. Toolchain

| | |
|---|---|
| `arduino-cli` | 1.5.1 |
| Core | `esp8266:esp8266` **3.1.2** (latest) |
| FQBN | `esp8266:esp8266:nodemcuv2` |
| Adafruit GFX | **1.12.6** (latest) |
| Adafruit ST7735/ST7789 | **1.11.0** (latest — still carries bug §5a) |
| Adafruit BusIO | **1.17.4** (latest) |
| Port | `/dev/ttyUSB0` (CP2102) · Serial **115200** |
| Board | ESP8266EX, 80 MHz, 4 MB flash |

All three libraries are at their newest published release; there is no upgrade
that fixes §5a.
