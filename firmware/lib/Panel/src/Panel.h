/* ============================================================================
 * Panel — the display driver for the reference build (ST7735 128x160 on an
 * ESP8266 NodeMcu). THIS FILE IS THE SINGLE HARDWARE CONFIGURATION POINT:
 * pins, panel geometry, init tab, rotation and SPI clock all live here, and
 * the sketches draw only through the Adafruit_GFX API this class exposes.
 *
 * USING DIFFERENT HARDWARE
 *   - Same ST7735 family, other size/tab: edit PANEL_W/PANEL_H/PANEL_TAB below.
 *   - Other Adafruit-supported panel (ST7789, ILI9341, ...): change the base
 *     class here and the initR()/init() call in Panel.cpp — the GFX drawing API
 *     is identical, so the sketches compile unchanged. The bulk-SPI fast paths
 *     write raw RGB565 and work for any panel with the same wire format.
 *   - Different MCU / entirely different display stack: the host does not care.
 *     Implement the two-line serial protocol (docs/PROTOCOL.md) any way you
 *     like; this whole directory is just the reference implementation.
 *
 * A thin subclass of Adafruit_ST7735, so you keep the entire Adafruit_GFX API
 * (text, fonts, shapes, bitmaps) and additionally get bulk-SPI fast paths that
 * Adafruit never implemented for the ESP8266.
 *
 * WHY THE FAST PATHS EXIST
 *   Adafruit_SPITFT has a bulk writeBytes() path for ESP32, nRF52, RP2040 and
 *   SAMD-DMA. On ESP8266 it falls through to a generic loop that issues ONE
 *   library call per pixel. That is call-overhead bound, so it plateaus around
 *   13% of the SPI ceiling no matter what clock you set (measured: 0.44 MB/s of
 *   a 3.33 MB/s ceiling). Pushing pixels through SPI.writeBytes() in 64-byte
 *   chunks — the ESP8266's hardware FIFO size — cuts per-call overhead ~32x and
 *   reaches 80% of the ceiling (measured: 2.67 MB/s, 65 fps full-screen).
 *
 *   fillScreen(), fillRect(), drawFastHLine() and drawFastVLine() are overridden
 *   to use it, so ordinary Adafruit_GFX code gets the speedup for free.
 *
 * See docs/HARDWARE.md (repo root) for the measured numbers and wiring gotchas.
 * ========================================================================== */

#pragma once

#include <Adafruit_GFX.h>
#include <Adafruit_ST7735.h>
#include <SPI.h>

/* ---- Wiring -----------------------------------------------------------------
 * MOSI and SCLK are FIXED by the ESP8266's HSPI peripheral and cannot be moved.
 * The three control pins are all boot-strapping pins; that is normal and is the
 * canonical ST7735 wiring, because the display's CS/DC/RST are high-impedance
 * inputs and do not fight the NodeMcu's strapping resistors.
 *
 *   Display  NodeMcu  GPIO
 *   RST      D4       2
 *   CS       D8       15
 *   DC       D3       0
 *   DIN      D7       13     MOSI, fixed
 *   CLK      D5       14     SCLK, fixed
 *   VCC      Vin      —      5V! the module regulates it down itself (see below)
 *   BL       3V3
 *   GND      GND
 *
 * VCC MUST BE 5V, NOT 3V3. The blue ST7735+SD module carries its own AMS1117-3.3
 * regulator and ships with solder jumper J1 OPEN, which means the VCC pin feeds
 * that regulator. An AMS1117 drops ~1.1V, so 3V3 in yields only ~2.2V out — too
 * little for the backlight LEDs to light. Symptom: the panel flashes on at power
 * up and then goes dark. (Short J1 if you want to feed 3V3 directly instead.)
 * -------------------------------------------------------------------------- */
#ifndef D3
  #define D3 0
  #define D4 2
  #define D8 15
#endif

#define PANEL_RST D4  // GPIO2
#define PANEL_CS  D8  // GPIO15
#define PANEL_DC  D3  // GPIO0

/* ---- Panel facts (measured on this hardware, see HARDWARE.md) ------------- */
#define PANEL_W        128   // rotation 0/2; rotation 1/3 swaps to 160x128
#define PANEL_H        160
#define PANEL_TAB      INITR_BLACKTAB               // NOT REDTAB: it is broken, see .cpp

/* This board is physically mounted inverted, so the default orientation is
 * rotation 2 — 180° from the ST7735's native 0, still 128x160 portrait (only
 * 1/3 swap to landscape). Everything, bitmaps included, flips wholesale via the
 * MADCTL setRotation() writes, so no artwork needs pre-flipping. Pass a rotation
 * explicitly to begin() to override (the selftest/probe examples do). */
#define PANEL_ROTATION 2

/* The ESP8266 divides 80 MHz by an integer, so only 40.0 / 26.7 / 20.0 / 16.0 /
 * 13.3 / 11.4 / 10.0 / 8.9 / 8.0 MHz actually exist. 26.7 MHz (ask for 27e6) is
 * the proven-stable default on dupont jumpers: 2.67 MB/s, 65 fps full-screen.
 * 40 MHz measured 3.66 MB/s / 89 fps here and is worth trying with short, tidy
 * wiring — watch a 1px checkerboard for sparkle before you trust it. */
#define PANEL_SPI_HZ 27000000

/* RAM REALITY CHECK. Free heap on this board is ~50 KB and a full framebuffer is
 * 40960 bytes. It "fits" but leaves ~9 KB for everything else, which is not a
 * budget you can build on. Stream bands instead: pushBand() below, or a
 * 128 x 16 band = 4096 bytes. */

class Panel : public Adafruit_ST7735 {
public:
  Panel() : Adafruit_ST7735(PANEL_CS, PANEL_DC, PANEL_RST) {}

  /* Hardware-reset pulse, init, clock, rotation, clear to black. Defaults to
   * PANEL_ROTATION (this board is mounted inverted); override per-call if needed. */
  void begin(uint32_t spiHz = PANEL_SPI_HZ, uint8_t rotation = PANEL_ROTATION);

  /* --- Adafruit_GFX overrides that use the bulk SPI FIFO ------------------ */
  void fillScreen(uint16_t color) override;
  void fillRect(int16_t x, int16_t y, int16_t w, int16_t h, uint16_t color) override;
  void drawFastHLine(int16_t x, int16_t y, int16_t w, uint16_t color) override;
  void drawFastVLine(int16_t x, int16_t y, int16_t h, uint16_t color) override;

  /* --- Bitmap blitting ----------------------------------------------------
   * Pixels must be RGB565 in BIG-ENDIAN byte order — that is the ST7735's wire
   * format, so the bytes stream straight out with no per-pixel work. Use
   * firmware/scripts/svg2rgb565.py, which emits exactly this layout. */
  void blitPROGMEM(int16_t x, int16_t y, int16_t w, int16_t h, const uint8_t *rgb565be);

  /* --- Band streaming -----------------------------------------------------
   * The memory-safe way to render on this chip: open a window, push it in
   * slices, close it. Nothing large is ever resident.
   *
   *   uint8_t band[128 * 16 * 2];
   *   panel.beginBand(0, 0, 128, 160);
   *   for (int y = 0; y < 160; y += 16) { render(band, y); panel.pushBand(band, sizeof band); }
   *   panel.endBand();
   */
  void beginBand(int16_t x, int16_t y, int16_t w, int16_t h);
  void pushBand(const uint8_t *rgb565be, size_t bytes);
  void endBand();

  /* 24-bit colour -> RGB565. */
  static inline uint16_t rgb(uint8_t r, uint8_t g, uint8_t b) {
    return ((r & 0xF8) << 8) | ((g & 0xFC) << 3) | (b >> 3);
  }

private:
  void bulkFill(uint32_t pixels, uint16_t color);   // assumes window already set
};

/* Brand colour used by the demos: #D97757 */
#define PANEL_BRAND 0xDBAA

extern Panel panel;
