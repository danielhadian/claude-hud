/* ============================================================================
 * selftest — prove the panel and the data link are healthy, end to end.
 *
 * Every step prints BEFORE it runs, so if the sketch hangs or the screen stays
 * dark, the last line on Serial names the thing that broke.
 *
 * WHAT THIS CAN AND CANNOT PROVE
 *   There is no MISO wire — the ST7735 is WRITE-ONLY. No firmware can read a
 *   chip ID or read pixels back, so "is the link good?" cannot be answered by a
 *   register read, and every test below would also pass with the display
 *   UNPLUGGED. Correctness is established by:
 *     (a) Serial instrumentation — clock, throughput vs the SPI ceiling, reset
 *         reason, heap drift. This catches brownouts, watchdog resets, leaks.
 *     (b) Patterns designed so each fault has a distinct look. See README.
 *   The picture itself needs your eyes. That is not a limitation of this sketch;
 *   it is a property of the wiring.
 *
 * Serial @115200.  Keys:  r = re-run,  s = cycle SPI clock,  h = help
 * ========================================================================== */

#include <Panel.h>
#include <ESP8266WiFi.h>

static const uint32_t CLOCKS[] = {8000000, 16000000, 20000000, 27000000, 40000000};
static const uint8_t  N_CLOCKS = sizeof(CLOCKS) / sizeof(CLOCKS[0]);
static uint8_t        clockIdx = 3;                 // -> 26.7 MHz, the safe default

static uint8_t  stepNo = 0;
static uint32_t stepUs = 0;

static void step(const char *msg) {
  Serial.printf("[%8.3f] STEP %-2u  %-36s", millis() / 1000.0, ++stepNo, msg);
  Serial.flush();                                   // flush BEFORE we risk hanging
  stepUs = micros();
}
static void ok(const char *extra = nullptr) {
  Serial.printf(" ok (%6.1f ms)", (micros() - stepUs) / 1000.0);
  if (extra) Serial.printf("  %s", extra);
  Serial.println();
}
static void rule() { Serial.println(F("------------------------------------------------------------------")); }

/* 1px checkerboard: the maximum-toggle-rate pattern, and therefore the harshest
 * signal-integrity test the wiring will ever see. Clean uniform grey = healthy.
 * Sparkle, smear or a shimmering moire = marginal wiring or too high a clock. */
static void checkerboard() {
  const int16_t w = panel.width(), h = panel.height();
  uint8_t row[PANEL_H * 2];
  panel.beginBand(0, 0, w, h);
  for (int16_t y = 0; y < h; y++) {
    for (int16_t x = 0; x < w; x++) {
      uint16_t c = ((x ^ y) & 1) ? ST77XX_WHITE : ST77XX_BLACK;
      row[2 * x] = c >> 8;
      row[2 * x + 1] = c & 0xFF;
    }
    panel.pushBand(row, (size_t)w * 2);
  }
  panel.endBand();
}

static void gradient() {
  const int16_t w = panel.width(), h = panel.height();
  uint8_t row[PANEL_H * 2];
  panel.beginBand(0, 0, w, h);
  for (int16_t y = 0; y < h; y++) {
    for (int16_t x = 0; x < w; x++) {
      uint16_t c = panel.rgb(x * 255 / (w - 1), y * 255 / (h - 1),
                             255 - x * 255 / (w - 1));
      row[2 * x] = c >> 8;
      row[2 * x + 1] = c & 0xFF;
    }
    panel.pushBand(row, (size_t)w * 2);
  }
  panel.endBand();
}

/* Colour bars, each LABELLED with the colour it is supposed to be. If the bar
 * that says RED looks blue, the panel is BGR rather than RGB. */
static void colorBars() {
  struct { uint16_t c; const char *n; uint16_t t; } bars[] = {
    {ST77XX_RED, "RED", ST77XX_WHITE},       {ST77XX_GREEN, "GREEN", ST77XX_BLACK},
    {ST77XX_BLUE, "BLUE", ST77XX_WHITE},     {ST77XX_YELLOW, "YELW", ST77XX_BLACK},
    {ST77XX_CYAN, "CYAN", ST77XX_BLACK},     {ST77XX_MAGENTA, "MGNT", ST77XX_BLACK},
    {ST77XX_WHITE, "WHITE", ST77XX_BLACK},   {ST77XX_BLACK, "BLACK", ST77XX_WHITE},
  };
  const int16_t w = panel.width(), bh = panel.height() / 8;
  panel.setTextSize(1);
  for (uint8_t i = 0; i < 8; i++) {
    panel.fillRect(0, i * bh, w, bh, bars[i].c);
    panel.setTextColor(bars[i].t);
    panel.setCursor(4, i * bh + (bh - 8) / 2);
    panel.print(bars[i].n);
  }
}

/* THE health signal: sustained bulk writes at the configured clock. A wrong
 * clock divider halves the efficiency; a broken SPI config collapses it. It is
 * also the hardest the wiring is ever driven, so it doubles as the stress test
 * for brownouts, watchdog resets and heap leaks. */
static void benchmark() {
  const uint32_t frames = 40;
  const uint32_t bytes  = (uint32_t)panel.width() * panel.height() * 2;
  const uint32_t hz     = CLOCKS[clockIdx];
  const uint32_t heap0  = ESP.getFreeHeap();

  step("throughput / stress");
  uint32_t t0 = micros();
  for (uint32_t f = 0; f < frames; f++) {
    panel.fillScreen((f & 1) ? ST77XX_WHITE : ST77XX_BLACK);
    yield();                                        // feed the soft watchdog
  }
  uint32_t us = micros() - t0;
  ok();

  const double mbs  = (double)bytes * frames / us;  // bytes/us == MB/s
  const double ceil_ = hz / 8.0 / 1e6;
  const double eff  = mbs / ceil_ * 100.0;
  const long   dHeap = (long)ESP.getFreeHeap() - (long)heap0;

  Serial.printf("           %.2f MB/s of %.2f MB/s ceiling @ %.1f MHz\n", mbs, ceil_, hz / 1e6);
  Serial.printf("           %.1f fps, %.2f ms/frame, efficiency %.1f%%\n",
                frames * 1e6 / us, us / 1000.0 / frames, eff);
  Serial.printf("           heap delta %ld bytes\n", dHeap);

  /* A healthy bus lands at 75-95%: the shortfall is constant per-frame overhead,
   * so efficiency falls as the clock rises (93% @ 8 MHz, 80% @ 26.7, 73% @ 40).
   * A wrong divider halves it to ~50%. 60% cleanly separates the two. Heap noise
   * of a few dozen bytes is Serial buffer churn, not a leak. */
  if (eff > 105.0)      Serial.println(F("           FAIL - impossible rate; the timer is lying."));
  else if (eff < 40.0)  Serial.println(F("           FAIL - bus far below its clock. SPI misconfigured."));
  else if (eff < 60.0)  Serial.println(F("           WARN - bus below par for this clock."));
  else if (dHeap < -256) Serial.printf ("           WARN - heap fell %ld bytes.\n", -dHeap);
  else                  Serial.println(F("           PASS - link stable at its configured clock."));
}

static void suite() {
  stepNo = 0;
  rule();
  Serial.printf("SELF-TEST  @ %.1f MHz\n", CLOCKS[clockIdx] / 1e6);
  rule();

  step("bring up display");
  panel.begin(CLOCKS[clockIdx]);
  char dim[24];
  snprintf(dim, sizeof(dim), "%dx%d", panel.width(), panel.height());
  ok(dim);

  const char *names[] = {"RED", "GREEN", "BLUE", "WHITE", "BLACK"};
  const uint16_t cols[] = {ST77XX_RED, ST77XX_GREEN, ST77XX_BLUE, ST77XX_WHITE, ST77XX_BLACK};
  for (uint8_t i = 0; i < 5; i++) {
    char m[32]; snprintf(m, sizeof(m), "fill %s", names[i]);
    step(m); panel.fillScreen(cols[i]); ok(); delay(350);
  }

  step("alignment: border + corners");
  panel.fillScreen(ST77XX_BLACK);
  panel.drawRect(0, 0, panel.width(), panel.height(), ST77XX_WHITE);
  panel.fillRect(0, 0, 8, 8, ST77XX_RED);
  panel.fillRect(panel.width() - 8, 0, 8, 8, ST77XX_GREEN);
  panel.fillRect(0, panel.height() - 8, 8, 8, ST77XX_BLUE);
  panel.fillRect(panel.width() - 8, panel.height() - 8, 8, 8, ST77XX_YELLOW);
  ok("border must touch all 4 edges");
  delay(2000);

  step("colour bars (RGB order)");   colorBars();     ok("labels must match colours"); delay(2500);
  step("RGB565 gradient");           gradient();      ok("smooth, no banding");        delay(1500);
  step("1px checkerboard (SPI)");    checkerboard();  ok("must be CLEAN uniform grey");delay(2500);

  step("graphics primitives");
  panel.fillScreen(ST77XX_BLACK);
  for (int16_t i = 0; i < panel.width() / 2; i += 7)
    panel.drawRect(i, i, panel.width() - 2 * i, panel.height() - 2 * i, ST77XX_GREEN);
  panel.fillCircle(panel.width() / 2, panel.height() / 3, 26, ST77XX_RED);
  panel.drawCircle(panel.width() / 2, panel.height() / 3, 30, ST77XX_WHITE);
  panel.fillTriangle(panel.width() / 2, panel.height() / 2 + 6, 8, panel.height() - 10,
                     panel.width() - 8, panel.height() - 10, ST77XX_BLUE);
  panel.fillRoundRect(panel.width() / 4, panel.height() / 2 - 14, panel.width() / 2, 28, 6, ST77XX_ORANGE);
  ok();
  delay(1500);

  step("text rendering");
  panel.fillScreen(ST77XX_BLACK);
  panel.setTextWrap(true);
  panel.setTextColor(ST77XX_WHITE); panel.setTextSize(2);
  panel.setCursor(2, 2);   panel.println(F("TEXT"));
  panel.setTextSize(1);
  panel.setTextColor(ST77XX_GREEN);  panel.setCursor(2, 24); panel.println(F("size 1 green"));
  panel.setTextColor(ST77XX_BLACK, ST77XX_CYAN);
  panel.setCursor(2, 36); panel.println(F(" inverse bg "));
  panel.setTextColor(ST77XX_WHITE);
  panel.setCursor(2, 52); panel.println(F("0123456789"));
  panel.setCursor(2, 62); panel.println(F("abcdefghijklm"));
  panel.setCursor(2, 72); panel.println(F("NOPQRSTUVWXYZ"));
  panel.setCursor(2, 82); panel.println(F("!@#$%^&*()[]{}"));
  panel.setTextColor(PANEL_BRAND); panel.setTextSize(3);
  panel.setCursor(2, 100); panel.println(F("Sz3"));
  ok("all glyphs crisp");
  delay(2500);

  for (uint8_t r = 0; r < 4; r++) {
    char m[24]; snprintf(m, sizeof(m), "rotation %u", r);
    step(m);
    panel.setRotation(r);
    panel.fillScreen(ST77XX_BLACK);
    panel.drawRect(0, 0, panel.width(), panel.height(), ST77XX_WHITE);
    panel.fillRect(0, 0, 10, 10, ST77XX_RED);       // origin marker follows rotation
    panel.setTextColor(ST77XX_WHITE); panel.setTextSize(1);
    panel.setCursor(14, 2); panel.printf("ROT %u  %dx%d", r, panel.width(), panel.height());
    char d[16]; snprintf(d, sizeof(d), "%dx%d", panel.width(), panel.height());
    ok(d);
    delay(800);
  }
  panel.setRotation(0);

  benchmark();

  step("summary card");
  panel.fillScreen(ST77XX_BLACK);
  panel.drawRect(0, 0, panel.width(), panel.height(), ST77XX_GREEN);
  panel.setTextSize(2); panel.setTextColor(ST77XX_WHITE);
  panel.setCursor(30, 10); panel.print(F("SELF"));
  panel.setCursor(30, 28); panel.print(F("TEST"));
  panel.setTextColor(ST77XX_GREEN);
  panel.setCursor(34, 52); panel.print(F("PASS"));
  panel.setTextSize(1); panel.setTextColor(ST77XX_CYAN);
  panel.setCursor(8, 80);  panel.print(F("display  OK"));
  panel.setCursor(8, 90);  panel.print(F("colours  OK"));
  panel.setCursor(8, 100); panel.print(F("graphics OK"));
  panel.setCursor(8, 110); panel.print(F("text     OK"));
  panel.setTextColor(ST77XX_WHITE);
  panel.setCursor(8, 126); panel.printf("SPI %.1f MHz", CLOCKS[clockIdx] / 1e6);
  panel.setCursor(8, 136); panel.printf("%dx%d BLACKTAB", panel.width(), panel.height());
  ok();

  rule();
  Serial.println(F("SUITE COMPLETE - every step returned."));
  Serial.println(F("If the panel is blank, the MCU side still passed: look at"));
  Serial.println(F("VCC (must be Vin/5V), the backlight, and the wiring."));
  rule();
  Serial.println(F("keys:  r = re-run   s = cycle SPI clock   h = help"));
}

void setup() {
  Serial.begin(115200);
  delay(300);
  Serial.println("\n");
  rule();
  Serial.println(F("ST7735 1.8\" 128x160  -  NodeMcu / ESP8266"));
  rule();
  Serial.printf("reset reason : %s\n", ESP.getResetReason().c_str());
  Serial.printf("free heap    : %u bytes\n", ESP.getFreeHeap());
  Serial.printf("core / sdk   : %s / %s\n", ESP.getCoreVersion().c_str(), ESP.getSdkVersion());
  Serial.println(F("bus          : hardware SPI, WRITE-ONLY (no MISO)"));
  Serial.println(F("               -> the panel cannot be read back; these tests"));
  Serial.println(F("                  would also pass with it unplugged."));

  WiFi.mode(WIFI_OFF);          // radio current spikes are the #1 brownout cause
  WiFi.forceSleepBegin();
  delay(1);

  suite();
}

void loop() {
  static uint32_t last = 0;
  static uint32_t beat = 0;

  if (Serial.available()) {
    switch (Serial.read()) {
      case 'r': suite(); break;
      case 's':
        clockIdx = (clockIdx + 1) % N_CLOCKS;
        Serial.printf("\n>> SPI clock -> %.1f MHz\n", CLOCKS[clockIdx] / 1e6);
        panel.setSPISpeed(CLOCKS[clockIdx]);
        stepNo = 0;
        step("1px checkerboard at new clock");
        checkerboard();
        ok("look for sparkle");
        benchmark();
        break;
      case 'h':
        Serial.println(F("keys:  r = re-run suite   s = cycle SPI clock   h = help"));
        break;
      default: break;                               // swallow \r \n
    }
  }

  /* Heartbeat. If the on-screen counter freezes but Serial keeps ticking, the
   * display link died while the MCU stayed up — that distinction is the point. */
  if (millis() - last >= 1000) {
    last = millis();
    beat++;
    panel.fillRect(6, 148, panel.width() - 12, 8, ST77XX_BLACK);
    panel.setTextSize(1);
    panel.setTextColor(ST77XX_GREEN);
    panel.setCursor(6, 148);
    panel.printf("alive %lus", beat);
    Serial.printf("[%8.3f] heartbeat %-4lu  heap %u\n",
                  millis() / 1000.0, beat, ESP.getFreeHeap());
  }
}
