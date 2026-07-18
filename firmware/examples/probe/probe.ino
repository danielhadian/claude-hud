/* ============================================================================
 * probe — measure the hardware instead of assuming it.
 *
 * Prints a capability report over Serial @115200, then draws an edge/ruler test
 * so the one thing firmware cannot know — are the outermost pixels actually
 * visible? — can be confirmed by eye.
 *
 * Re-run this whenever the hardware changes. The numbers in HARDWARE.md came
 * from exactly this sketch.
 * ========================================================================== */

#include <Panel.h>
#include <ESP8266WiFi.h>

// The ESP8266 divides 80 MHz by an integer, so only these rates really exist.
static const uint32_t CLOCKS[] = {8000000, 10000000, 13300000, 16000000,
                                  20000000, 27000000, 40000000};
static const uint8_t  N_CLOCKS = sizeof(CLOCKS) / sizeof(CLOCKS[0]);

// Largest block we can actually allocate — the true ceiling for a framebuffer.
static uint32_t largestAlloc() {
  uint32_t lo = 0, hi = 64 * 1024;
  while (lo < hi) {
    uint32_t mid = (lo + hi + 1) / 2;
    void *p = malloc(mid);
    if (p) { free(p); lo = mid; } else { hi = mid - 1; }
  }
  return lo;
}

void setup() {
  Serial.begin(115200);
  delay(400);
  Serial.println(F("\n\n=========== ST7735 / ESP8266 CAPABILITY PROBE ==========="));

  WiFi.mode(WIFI_OFF);
  WiFi.forceSleepBegin();
  delay(1);

  Serial.println(F("\n[MCU]"));
  Serial.printf("  chip id           : 0x%06X\n", ESP.getChipId());
  Serial.printf("  cpu               : %u MHz\n", ESP.getCpuFreqMHz());
  Serial.printf("  core / sdk        : %s / %s\n",
                ESP.getCoreVersion().c_str(), ESP.getSdkVersion());
  Serial.printf("  flash size        : %u bytes @ %u Hz\n",
                ESP.getFlashChipRealSize(), ESP.getFlashChipSpeed());
  Serial.printf("  sketch / free     : %u / %u bytes\n",
                ESP.getSketchSize(), ESP.getFreeSketchSpace());
  Serial.printf("  reset reason      : %s\n", ESP.getResetReason().c_str());

  const uint32_t heap = ESP.getFreeHeap();
  Serial.println(F("\n[RAM]  <- decides whether a framebuffer is possible at all"));
  Serial.printf("  free heap         : %u bytes\n", heap);
  Serial.printf("  max free block    : %u bytes\n", ESP.getMaxFreeBlockSize());
  Serial.printf("  fragmentation     : %u %%\n", ESP.getHeapFragmentation());
  Serial.printf("  largest malloc    : %u bytes (measured by bisection)\n", largestAlloc());

  Serial.println(F("\n[PANEL]"));
  panel.begin();
  for (uint8_t r = 0; r < 4; r++) {
    panel.setRotation(r);
    Serial.printf("  rotation %u        : %3d x %3d px\n", r, panel.width(), panel.height());
  }
  panel.setRotation(0);
  const uint32_t W = panel.width(), H = panel.height(), FB = W * H * 2;
  Serial.printf("  colour format     : RGB565, 16 bpp (5R 6G 5B)\n");
  Serial.printf("  total pixels      : %lu\n", W * H);
  Serial.printf("  FULL framebuffer  : %lu bytes\n", FB);
  Serial.printf("  ...vs free heap   : %u -> %s\n", heap,
                FB < heap ? "fits, but leaves almost nothing" : "DOES NOT FIT");
  Serial.printf("  128x16 band       : %u bytes (a safe streaming buffer)\n", 128 * 16 * 2);

  Serial.println(F("\n[SPI CLOCK SWEEP]  20 full frames per rate, bulk FIFO writes"));
  Serial.println(F("  requested   MB/s   ceiling   eff     fps   ms/frame"));
  for (uint8_t i = 0; i < N_CLOCKS; i++) {
    panel.setSPISpeed(CLOCKS[i]);
    const uint32_t frames = 20;
    uint32_t t0 = micros();
    for (uint32_t f = 0; f < frames; f++) {
      panel.fillScreen((f & 1) ? ST77XX_WHITE : ST77XX_BLACK);
      yield();
    }
    uint32_t us = micros() - t0;
    double mbs = (double)FB * frames / us;
    double ceil_ = CLOCKS[i] / 8.0 / 1e6;
    Serial.printf("  %5.1f MHz   %5.2f   %5.2f   %5.1f%%  %5.1f  %6.2f\n",
                  CLOCKS[i] / 1e6, mbs, ceil_, mbs / ceil_ * 100.0,
                  frames * 1e6 / us, us / 1000.0 / frames);
  }

  // ---- Edge + ruler test: the part only your eyes can settle ---------------
  panel.setSPISpeed(PANEL_SPI_HZ);
  panel.fillScreen(ST77XX_BLACK);
  panel.drawRect(0, 0, W, H, ST77XX_WHITE);          // 1px border, extreme edge
  panel.drawPixel(0, 0, ST77XX_RED);
  panel.drawPixel(W - 1, 0, ST77XX_GREEN);
  panel.drawPixel(0, H - 1, ST77XX_BLUE);
  panel.drawPixel(W - 1, H - 1, ST77XX_YELLOW);
  for (int16_t x = 0; x < (int16_t)W; x += 10)
    panel.drawFastVLine(x, 2, (x % 50 == 0) ? 8 : 4, ST77XX_CYAN);
  for (int16_t y = 0; y < (int16_t)H; y += 10)
    panel.drawFastHLine(2, y, (y % 50 == 0) ? 8 : 4, ST77XX_CYAN);
  panel.setTextColor(ST77XX_WHITE);
  panel.setTextSize(1);
  panel.setCursor(16, 70);  panel.printf("%lux%lu", W, H);
  panel.setCursor(16, 82);  panel.print(F("BLACKTAB"));
  panel.setCursor(16, 94);  panel.print(F("border must"));
  panel.setCursor(16, 104); panel.print(F("touch edges"));

  Serial.println(F("\n[EDGE TEST - needs your eyes]"));
  Serial.println(F("  1px white border at the extreme edge, one coloured pixel per corner:"));
  Serial.println(F("    top-left RED    top-right GREEN"));
  Serial.println(F("    bot-left BLUE   bot-right YELLOW"));
  Serial.println(F("  All 4 corners + all 4 edges visible => offsets are 0,0 and the"));
  Serial.println(F("  full 128x160 is addressable. A missing edge or a coloured band"));
  Serial.println(F("  means the offsets are wrong for this panel."));
  Serial.println(F("\n===================== END OF PROBE ====================="));
}

void loop() {}
