#include "Panel.h"

Panel panel;

/* One 64-byte burst == the ESP8266's SPI hardware FIFO == 32 RGB565 pixels. */
static const uint8_t FIFO_BYTES  = 64;
static const uint8_t FIFO_PIXELS = FIFO_BYTES / 2;

void Panel::begin(uint32_t spiHz, uint8_t rotation) {
  /* Drive RST by hand before handing the pin to the library, so the panel starts
   * from a known state rather than whatever the last sketch left it in. The
   * ST7735 needs >120ms after reset before it will accept commands. */
  pinMode(PANEL_RST, OUTPUT);
  digitalWrite(PANEL_RST, HIGH); delay(10);
  digitalWrite(PANEL_RST, LOW);  delay(20);
  digitalWrite(PANEL_RST, HIGH); delay(150);

  /* PANEL_TAB is BLACKTAB and must not be changed to INITR_REDTAB.
   *
   * Adafruit ST7735 1.11.0 (still true on upstream master) ships a constant
   * collision in Adafruit_ST7735.h:
   *     #define INITR_REDTAB      0x01
   *     #define INITR_144GREENTAB 0x01     <-- should be 0x03
   * initR() tests 144GREENTAB first, so asking for REDTAB silently runs the
   * 1.44" init sequence and forces the panel to 128x128 — you lose the bottom
   * 32 rows. Verified on this board: initR(INITR_REDTAB) -> "128 x 128 px".
   *
   * REDTAB differs from BLACKTAB only in the colour filter (BGR vs RGB), and
   * the offsets are identical, so nothing is actually lost by avoiding it. */
  initR(PANEL_TAB);

  setSPISpeed(spiHz);
  setRotation(rotation);
  fillScreen(ST77XX_BLACK);
}

/* Fill `pixels` pixels into an address window that the caller has already set,
 * inside a transaction the caller has already opened. */
void Panel::bulkFill(uint32_t pixels, uint16_t color) {
  uint8_t buf[FIFO_BYTES];
  const uint8_t hi = color >> 8, lo = color & 0xFF;
  for (uint8_t i = 0; i < FIFO_PIXELS; i++) {
    buf[2 * i] = hi;
    buf[2 * i + 1] = lo;
  }
  uint32_t bursts = pixels / FIFO_PIXELS;
  uint32_t rem    = pixels % FIFO_PIXELS;
  while (bursts--) SPI.writeBytes(buf, FIFO_BYTES);
  if (rem) SPI.writeBytes(buf, rem * 2);
}

void Panel::fillScreen(uint16_t color) {
  startWrite();
  setAddrWindow(0, 0, width(), height());
  bulkFill((uint32_t)width() * height(), color);
  endWrite();
}

void Panel::fillRect(int16_t x, int16_t y, int16_t w, int16_t h, uint16_t color) {
  // Clip to the panel; an unclipped setAddrWindow would wrap and corrupt the frame.
  if (w <= 0 || h <= 0) return;
  if (x < 0) { w += x; x = 0; }
  if (y < 0) { h += y; y = 0; }
  if (x >= width() || y >= height()) return;
  if (x + w > width())  w = width()  - x;
  if (y + h > height()) h = height() - y;
  if (w <= 0 || h <= 0) return;

  startWrite();
  setAddrWindow(x, y, w, h);
  bulkFill((uint32_t)w * h, color);
  endWrite();
}

void Panel::drawFastHLine(int16_t x, int16_t y, int16_t w, uint16_t color) {
  fillRect(x, y, w, 1, color);
}

void Panel::drawFastVLine(int16_t x, int16_t y, int16_t h, uint16_t color) {
  fillRect(x, y, 1, h, color);
}

/* Reject a destination rect that isn't fully on-screen. Like fillRect's clip,
 * this stops an out-of-bounds setAddrWindow from wrapping and corrupting the
 * frame; unlike fillRect it can't partial-clip (that needs a per-row source
 * offset), so an off-screen blit is skipped whole. Every current caller passes
 * on-screen coords, so this is defensive only. */
static inline bool blitFits(int16_t x, int16_t y, int16_t w, int16_t h,
                            int16_t panelW, int16_t panelH) {
  return w > 0 && h > 0 && x >= 0 && y >= 0 && x + w <= panelW && y + h <= panelH;
}

void Panel::blitPROGMEM(int16_t x, int16_t y, int16_t w, int16_t h, const uint8_t *rgb565be) {
  /* PROGMEM lives in flash and cannot be handed to SPI directly, so copy a row
   * at a time into RAM. One memcpy_P + one bulk write per row; no per-pixel work
   * because the blob is already in the panel's byte order. */
  if (!blitFits(x, y, w, h, width(), height())) return;
  const size_t rowBytes = (size_t)w * 2;
  uint8_t row[PANEL_H * 2];              // 320 B — big enough for any rotation
  if (rowBytes > sizeof(row)) return;

  startWrite();
  setAddrWindow(x, y, w, h);
  for (int16_t r = 0; r < h; r++) {
    memcpy_P(row, rgb565be + (uint32_t)r * rowBytes, rowBytes);
    SPI.writeBytes(row, rowBytes);
  }
  endWrite();
}

void Panel::beginBand(int16_t x, int16_t y, int16_t w, int16_t h) {
  startWrite();
  setAddrWindow(x, y, w, h);
}

void Panel::pushBand(const uint8_t *rgb565be, size_t bytes) {
  SPI.writeBytes(const_cast<uint8_t *>(rgb565be), bytes);
}

void Panel::endBand() {
  endWrite();
}
