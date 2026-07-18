/* ============================================================================
 * logo — render a vector mark on the panel.
 *
 * The ESP8266 cannot parse SVG, so the rasterising happens on the host:
 *   scripts/svg2rgb565.py  ->  logo.h
 *
 * The source path is entirely axis-aligned (M/H/h/V/v/z), i.e. a rectilinear
 * polygon with two holes, so a scanline even-odd fill reproduces it exactly;
 * supersampling gives anti-aliased edges, pre-blended against black. The blob is
 * emitted as RGB565 in big-endian byte order — the ST7735's own wire format — so
 * each row streams from flash to SPI with no per-pixel work at all.
 * ========================================================================== */

#include <Panel.h>
#include <ESP8266WiFi.h>
#include "logo.h"

void setup() {
  Serial.begin(115200);
  delay(300);
  Serial.printf("\nfree heap : %u bytes\n", ESP.getFreeHeap());
  Serial.printf("image     : %dx%d RGB565, %u bytes in flash\n",
                LOGO_W, LOGO_H, (unsigned)sizeof(LOGO));

  WiFi.mode(WIFI_OFF);
  WiFi.forceSleepBegin();
  delay(1);

  panel.begin();
  Serial.printf("panel     : %dx%d\n", panel.width(), panel.height());

  const int16_t x = (panel.width() - LOGO_W) / 2;    // -> 8
  const int16_t y = 16;

  uint32_t t0 = micros();
  panel.blitPROGMEM(x, y, LOGO_W, LOGO_H, LOGO);
  uint32_t us = micros() - t0;
  Serial.printf("blit      : %.1f ms  (%.2f MB/s)\n",
                us / 1000.0, (double)sizeof(LOGO) / us);

  panel.setTextSize(1);
  panel.setTextColor(PANEL_BRAND);
  panel.setCursor(31, 138);                          // centred: 11 chars * 6px
  panel.print(F("Claude Code"));

  Serial.println(F("done."));
}

void loop() {
  // Breathing underline: proves the link is live without redrawing the mark.
  static uint32_t last = 0;
  static int16_t  w = 0, dir = 2;
  if (millis() - last < 40) return;
  last = millis();

  panel.drawFastHLine(28, 152, 72, ST77XX_BLACK);
  panel.drawFastHLine(64 - w / 2, 152, w, PANEL_BRAND);
  w += dir;
  if (w >= 72 || w <= 0) dir = -dir;
}
