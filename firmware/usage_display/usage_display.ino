/* ============================================================================
 * usage_display — show live Claude usage on the ST7735, fed over USB serial.
 *
 * The laptop tool (tools/claude-usage) runs as Claude Code's statusline and
 * pushes one compact line per signal over the USB cable; this firmware caches
 * and renders it. No WiFi, no HTTP, no image path — the board does one thing.
 *
 *   @U|five=6|seven=36|sub=Max 20x|five_s=3120|seven_s=280800   usage
 *   @S|sessions=2|busy=1                                        activity
 *
 * five_s/seven_s are SECONDS REMAINING until each limit resets. The host sends
 * seconds, not a baked "52m" string, and tickResets() counts them down locally
 * — so the panel stays truthful even when nothing is pushing to it.
 * ========================================================================== */

#include <Panel.h>
#include <math.h>
#include "mascot.h"
#include "logo_boot.h"
#include <Fonts/FreeSansBold9pt7b.h>

static const uint16_t COL_FIG = 0xF75C;   // warm white for the big figures

#define FIRMWARE_VERSION "2.2.0"

/* Cap received reset seconds well past any real window (weekly ≈ 7 days). A
 * negative or garbage value on a stray line would otherwise strtoul-wrap to
 * ~4.29e9 and render as "49710d"; clamping keeps the countdown sane. */
static const uint32_t MAX_RESET_SEC = 32UL * 86400UL;   // 32 days

/* Session data expires. The host pushes @S every ~10 s while any Claude Code
 * session is open, and its own activity windows decay in 30-60 s — so after
 * 2 minutes of silence the truthful reading is "no active sessions", not
 * last-known dots pulsing "busy" forever. This also cleans up the holder's
 * cache replay after a reboot, which can carry a stale @S. Usage percentages
 * deliberately do NOT decay: there is no truthful value to decay them to,
 * and their reset countdowns already stay honest via tickResets(). */
static const uint32_t SESS_DECAY_MS = 120000UL;   // 2 min without any @S line

/* ============================================================================
 * Claude usage limits.  Nothing here is hardcoded — every value is whatever the
 * host last sent. The board only caches, counts down, and renders.
 * ========================================================================== */
struct Usage {
  bool     valid       = false;   // have we ever received a line?
  uint8_t  fivePct     = 0;
  uint8_t  sevenPct    = 0;
  char     sub[13]     = "";      // subscription tier, e.g. "Max 20x"
  char     fiveReset[12] = "";    // rendered "52m", "3d 4h" - derived, see tickResets
  char     sevenReset[12] = "";
  uint32_t fiveResetSec  = 0;     // seconds to reset at the moment of receipt
  uint32_t sevenResetSec = 0;
  uint32_t fiveResetBaseMs  = 0;  // millis() when fiveResetSec was received
  uint32_t sevenResetBaseMs = 0;  // millis() when sevenResetSec was received
  bool     fiveResetValid  = false;   // has five_s ever been received?
  bool     sevenResetValid = false;   // has seven_s ever been received?
};
static Usage usage;

/* Claude Code session activity, pushed by the host as:  @S|sessions=2|busy=1 */
struct Sessions {
  bool     valid     = false;
  uint8_t  total     = 0;   // live Claude Code sessions
  uint8_t  busy      = 0;   // of those, how many are actively working
  uint32_t rxMs      = 0;   // millis() of the last @S line — drives SESS_DECAY_MS
};
static Sessions sessions;

/* Dirty-region flags. Each update sets ONLY the bits whose field actually
 * changed, so a data tick repaints just that region — an unchanged re-push
 * (the statusline re-sends every ~10 s) repaints nothing at all. */
#define UD_ALL   0x01   // full layout — first entry to the usage view
#define UD_TIER  0x02   // subscription pill
#define UD_VAL5  0x04   // the 5-hour meter (only)
#define UD_VAL7  0x08   // the weekly meter (only)
#define UD_SESS  0x10   // session dot-row
static uint8_t usageDirty = UD_ALL;

/* --- serial line assembly ------------------------------------------------- */
static char    serialLine[160];
static uint8_t serialLen     = 0;
static bool    serialDiscard = false;   // overlong line in progress: swallow to \n

/* ================================================================ usage view
 * "Refined bars": warm coral on near-black.
 *
 *   ┌───────────────────────────┐
 *   │  (mascot)        Max 20x  │   30px mascot, top-left; tier pill, right
 *   │  5-HOUR              45%   │   small caption (left) + bold value (right)
 *   │  ▓▓▓▓▓▓▓▓░░░░░░░░░░░░░░░   │   rounded meter track + coral fill
 *   │  resets 52m               │
 *   │  WEEKLY             64%   │
 *   │  ▓▓▓▓▓▓▓▓▓▓▓▓▓░░░░░░░░░   │
 *   │  resets 3d 4h             │
 *   │ ─────────────────────────  │
 *   │  SESSIONS  ● ● ○ ○         │   one dot per session; busy filled + pulsing
 *   └───────────────────────────┘                        idle hollow
 * ========================================================================== */
static const uint16_t COL_DIM   = 0x8410;   // mid grey
static const uint16_t COL_EDGE  = 0x39C7;   // outlines, dark grey
static const uint16_t COL_TRACK = 0x18E3;   // empty meter, near-black
static const uint16_t COL_CREST = 0xFDF4;   // brightest coral — the fill surface

/* Coral ramp, dark -> light, for the "breathing" pulse of the busy dots. */
static const uint16_t CORAL_RAMP[5] = {0x7A06, 0xAAE8, 0xDBAA, 0xF4AF, 0xFDF4};

/* ---- layout ---------------------------------------------------------------
 * Derived from PANEL_W/PANEL_H (Panel.h) where that is safe: widths and the
 * bottom-anchored session row adapt to other panel sizes; the top-anchored
 * header and meter rows are tuned for a >=128x160 portrait panel and stay
 * absolute. A larger panel gets its extra space between the meters and the
 * session row. (See docs/PROTOCOL.md to target very different displays.)
 *
 * struct BarBox must be defined before pulseColor() (the file's first function)
 * because .ino auto-prototyping hoists forward declarations to just above it. */
static const int16_t MARK_X = 5,  MARK_Y = 3, HDR_H = 30;   // 30px mascot, top-left
static const int16_t TIER_H = 15, TIER_Y = 3 + (30 - 15) / 2;

static const int16_t BAR_X = 8, BAR_W = PANEL_W - 2 * BAR_X;
static const int16_t RIGHT_X = BAR_X + BAR_W;
static const int16_t TRACK_H = 10, TRACK_R = 5;
struct BarBox { int16_t numY, trackY, resetY; };   // numY = value baseline
static const BarBox B5 = {50, 58, 70};             // 5-hour
static const BarBox B7 = {96, 104, 116};           // weekly

static const int16_t DIV_Y  = PANEL_H - 28;        // hairline above sessions
static const int16_t SESS_Y = PANEL_H - 15;        // caption top / dot band
static const int16_t DOT_X0 = 66;                  // first dot centre (after the caption)
static const int16_t DOT_Y  = PANEL_H - 12;
static const int16_t DOT_R = 3, DOT_SP = 9;
/* As many dots as fit, keeping 8px on the right for the "+" overflow marker. */
static const int16_t DOT_MAX = (PANEL_W - DOT_X0 - 8) / DOT_SP;

/* Smoothly cycle the coral ramp from the animation phase. */
static uint16_t pulseColor(float phase) {
  float t = 0.5f + 0.5f * sinf(phase * 0.9f);      // 0..1
  int idx = (int)lroundf(t * 4.0f);
  if (idx < 0) idx = 0;
  if (idx > 4) idx = 4;
  return CORAL_RAMP[idx];
}

/* Static header: the mascot, top-left. Drawn once (UD_ALL). */
static void drawHeader() {
  panel.blitPROGMEM(MARK_X, MARK_Y, MASCOT_W, MASCOT_H, MASCOT);
}

/* Tier badge: a coral outline pill sized to the label, right-aligned in the
 * header. (Built-in font; glyph advance = 6px.) */
static void drawTier() {
  panel.fillRect(MARK_X + MASCOT_W + 2, MARK_Y,    // clear the header's right zone
                 panel.width() - (MARK_X + MASCOT_W + 2), HDR_H, ST77XX_BLACK);
  if (!(usage.valid && usage.sub[0])) return;
  int tw    = (int)strlen(usage.sub) * 6 - 1;
  int pillW = tw + 12;
  int px    = panel.width() - 6 - pillW;           // 6px right margin
  panel.drawRoundRect(px, TIER_Y, pillW, TIER_H, TIER_H / 2, PANEL_BRAND);
  panel.setFont();
  panel.setTextSize(1);
  panel.setTextColor(PANEL_BRAND);
  panel.setCursor(px + 6, TIER_Y + (TIER_H - 7) / 2 + 1);
  panel.print(usage.sub);
}

/* One usage meter: small caption (left), bold value (right), rounded track +
 * coral fill, tiny reset line. Clears its own band first so a shrinking value
 * leaves no stale pixels (custom-font glyphs don't paint their background). */
static void drawBar(const BarBox &b, const char *label, uint8_t pct,
                    const char *reset) {
  panel.fillRect(0, b.numY - 14, panel.width(),
                 (b.resetY + 8) - (b.numY - 14), ST77XX_BLACK);

  panel.setFont();                                 // caption: built-in, dim
  panel.setTextSize(1);
  panel.setTextColor(COL_DIM);
  panel.setCursor(BAR_X, b.numY - 6);
  panel.print(label);

  char num[6];                                     // value: bold 9pt, right-aligned
  snprintf(num, sizeof num, "%u", pct);
  int16_t x1, y1; uint16_t wN, hN, wP, hP;
  panel.setFont(&FreeSansBold9pt7b);
  panel.getTextBounds("%", 0, b.numY, &x1, &y1, &wP, &hP);
  panel.getTextBounds(num, 0, b.numY, &x1, &y1, &wN, &hN);
  panel.setTextColor(COL_FIG);
  panel.setCursor(RIGHT_X - wP - wN - 2, b.numY);
  panel.print(num);
  panel.setTextColor(PANEL_BRAND);
  panel.setCursor(RIGHT_X - wP, b.numY);
  panel.print("%");
  panel.setFont();

  panel.drawRoundRect(BAR_X, b.trackY, BAR_W, TRACK_H, TRACK_R, COL_EDGE);
  panel.fillRoundRect(BAR_X + 1, b.trackY + 1, BAR_W - 2, TRACK_H - 2,
                      TRACK_R - 1, COL_TRACK);
  int16_t fillW = (int32_t)pct * (BAR_W - 2) / 100;
  if (fillW > 0) {
    panel.fillRoundRect(BAR_X + 1, b.trackY + 1, fillW, TRACK_H - 2,
                        TRACK_R - 1, PANEL_BRAND);
    if (fillW > 3)
      panel.drawFastHLine(BAR_X + 2, b.trackY + 2, fillW - 2, COL_CREST);
  }

  panel.setTextSize(1);                            // reset line (built-in, tiny)
  panel.setTextColor(COL_DIM);
  panel.setCursor(BAR_X, b.resetY);
  if (usage.valid && reset[0]) {
    char r[24];
    snprintf(r, sizeof r, "resets %s", reset);
    panel.print(r);
  } else if (!usage.valid) {
    panel.print(F("waiting for data"));
  }
}

/* Session row: a caption + ONE dot per session — busy filled (these pulse),
 * idle hollow. Accurate up to DOT_MAX; a trailing "+" marks any overflow. */
static void drawSessions() {
  panel.fillRect(0, DIV_Y, panel.width(), panel.height() - DIV_Y, ST77XX_BLACK);
  panel.drawFastHLine(BAR_X, DIV_Y, BAR_W, COL_TRACK);

  panel.setFont();
  panel.setTextSize(1);
  panel.setTextColor(COL_DIM);
  panel.setCursor(BAR_X, SESS_Y);
  panel.print("SESSIONS");

  int total = sessions.valid ? sessions.total : 0;
  int busy  = sessions.valid ? sessions.busy  : 0;
  int shown = total > DOT_MAX ? DOT_MAX : total;

  int16_t cx = DOT_X0;
  for (int i = 0; i < shown; i++) {
    if (i < busy) panel.fillCircle(cx, DOT_Y, DOT_R, PANEL_BRAND);
    else          panel.drawCircle(cx, DOT_Y, DOT_R, COL_DIM);
    cx += DOT_SP;
  }
  if (!sessions.valid) {
    panel.setCursor(DOT_X0 - DOT_R, SESS_Y); panel.print("connecting");
  } else if (total == 0) {
    panel.setCursor(DOT_X0 - DOT_R, SESS_Y); panel.print("none");
  } else if (total > DOT_MAX) {
    panel.setCursor(cx - 1, SESS_Y); panel.print("+");
  }
}

/* Re-fill just the busy dots with the pulsing coral each frame. */
static void pulseDots(float phase) {
  if (!sessions.valid || sessions.busy == 0) return;
  uint16_t hot = pulseColor(phase);
  int shown = sessions.busy;
  if (shown > sessions.total) shown = sessions.total;
  if (shown > DOT_MAX)        shown = DOT_MAX;
  int16_t cx = DOT_X0;
  for (int i = 0; i < shown; i++) { panel.fillCircle(cx, DOT_Y, DOT_R, hot); cx += DOT_SP; }
}

/* "3d 4h" / "1h 43m" / "52m" — human-readable remaining time from a second count. */
static void formatRemaining(uint32_t sec, char *out, size_t n) {
  if (sec >= 86400UL)      snprintf(out, n, "%lud %luh", sec / 86400UL, (sec % 86400UL) / 3600UL);
  else if (sec >= 3600UL)  snprintf(out, n, "%luh %lum", sec / 3600UL, (sec % 3600UL) / 60UL);
  else                     snprintf(out, n, "%lum", sec / 60UL);
}

/* Recompute the reset strings from the last received seconds + elapsed millis.
   Called once per frame; the recompute is two snprintf+strcmp, and it dirties a
   meter only when the *rendered* text changes (~once a minute), so an actual
   repaint is rare. Unsigned subtraction wraps correctly at the 49-day millis()
   rollover. */
static void tickResets() {
  if (!usage.valid) return;
  char buf[12];

  if (usage.fiveResetValid) {
    uint32_t elapsed = (millis() - usage.fiveResetBaseMs) / 1000UL;
    uint32_t f = usage.fiveResetSec > elapsed ? usage.fiveResetSec - elapsed : 0;
    formatRemaining(f, buf, sizeof buf);
    if (strcmp(buf, usage.fiveReset)) {
      strlcpy(usage.fiveReset, buf, sizeof usage.fiveReset);
      usageDirty |= UD_VAL5;
    }
  }

  if (usage.sevenResetValid) {
    uint32_t elapsed = (millis() - usage.sevenResetBaseMs) / 1000UL;
    uint32_t s = usage.sevenResetSec > elapsed ? usage.sevenResetSec - elapsed : 0;
    formatRemaining(s, buf, sizeof buf);
    if (strcmp(buf, usage.sevenReset)) {
      strlcpy(usage.sevenReset, buf, sizeof usage.sevenReset);
      usageDirty |= UD_VAL7;
    }
  }
}

/* Decay session dots to "none" after SESS_DECAY_MS without an @S line — see the
 * constant's comment for why this is the truthful rendering. Unsigned millis()
 * subtraction wraps correctly at rollover. Once decayed, this is idempotent
 * (total/busy already 0), so it dirties UD_SESS exactly once per outage. */
static void tickSessionDecay() {
  if (!sessions.valid || (sessions.total == 0 && sessions.busy == 0)) return;
  if (millis() - sessions.rxMs < SESS_DECAY_MS) return;
  sessions.total = 0;
  sessions.busy  = 0;
  usageDirty |= UD_SESS;
  Serial.println(F("sessions: decayed to none (no @S in 2 min)"));
}

/* Redraw only the dirty regions. UD_ALL lays out everything on first entry;
 * after that each field repaints on its own bit and nothing else moves. */
static void renderUsage() {
  if (usageDirty & UD_ALL) {
    panel.fillScreen(ST77XX_BLACK);
    drawHeader();
    drawTier();
    drawBar(B5, "5-HOUR", usage.fivePct,  usage.fiveReset);
    drawBar(B7, "WEEKLY", usage.sevenPct, usage.sevenReset);
    drawSessions();
    usageDirty = 0;
    return;
  }
  if (usageDirty & UD_TIER) drawTier();
  if (usageDirty & UD_VAL5) drawBar(B5, "5-HOUR", usage.fivePct,  usage.fiveReset);
  if (usageDirty & UD_VAL7) drawBar(B7, "WEEKLY", usage.sevenPct, usage.sevenReset);
  if (usageDirty & UD_SESS) drawSessions();
  usageDirty = 0;
}

static void animateUsageFrame() {
  static float phase = 0.0f;
  if (usageDirty) renderUsage();
  // Wrap on pulseColor()'s period (sinf(phase*0.9) repeats every 2*PI/0.9), so
  // the accumulator can't grow until 0.15 falls below a float ulp (~43 days) and
  // the pulse silently freezes.
  phase = fmodf(phase + 0.15f, (float)TWO_PI / 0.9f);
  pulseDots(phase);      // only the busy dots animate; everything else is static
}

/* Parse one host line: "@U|five=6|seven=36|sub=Max 20x|five_s=3120|seven_s=280800".
 * Tolerant of missing fields and any key order — the host omits a window's fields
 * when that rate limit is absent, and omission means "unchanged", which is why
 * `next` starts as a copy of the current state. Sets a dirty bit per field that
 * actually changed. */
static void parseUsageLine(char *line) {
  Usage next = usage;                 // keep prior values for absent fields

  char *save = nullptr;
  char *tok = strtok_r(line, "|", &save);   // first token is the "@U" prefix
  while ((tok = strtok_r(nullptr, "|", &save)) != nullptr) {
    char *eq = strchr(tok, '=');
    if (!eq) continue;
    *eq = '\0';
    const char *key = tok, *val = eq + 1;

    if (!strcmp(key, "five"))            next.fivePct  = (uint8_t)constrain(atoi(val), 0, 100);
    else if (!strcmp(key, "seven"))      next.sevenPct = (uint8_t)constrain(atoi(val), 0, 100);
    else if (!strcmp(key, "sub"))        strlcpy(next.sub, val, sizeof next.sub);
    else if (!strcmp(key, "five_s"))  { unsigned long s = strtoul(val, nullptr, 10);
                                        next.fiveResetSec    = s > MAX_RESET_SEC ? MAX_RESET_SEC : (uint32_t)s;
                                        next.fiveResetBaseMs = millis();
                                        next.fiveResetValid  = true; }
    else if (!strcmp(key, "seven_s")) { unsigned long s = strtoul(val, nullptr, 10);
                                        next.sevenResetSec    = s > MAX_RESET_SEC ? MAX_RESET_SEC : (uint32_t)s;
                                        next.sevenResetBaseMs = millis();
                                        next.sevenResetValid  = true; }
  }

  uint8_t dirty = 0;                  // repaint only the fields that moved
  if (!usage.valid || strcmp(next.sub, usage.sub) != 0)
    dirty |= UD_TIER;
  // NB: the reset SECONDS are deliberately NOT part of this test. They drop by
  // ~10 on every re-push, so keying the dirty bit off them would repaint (and
  // visibly flash) both meters every ~10 s for no visible change. tickResets()
  // owns reset invalidation: it dirties a meter only when the *rendered* string
  // changes (~once a minute). First paint still fires via !usage.valid, and a
  // genuine jump in the countdown is caught by tickResets in the same frame.
  if (!usage.valid || next.fivePct != usage.fivePct)
    dirty |= UD_VAL5;
  if (!usage.valid || next.sevenPct != usage.sevenPct)
    dirty |= UD_VAL7;

  next.valid = true;
  usage = next;
  usageDirty |= dirty;
  if (dirty)
    Serial.printf("usage: five=%u%% seven=%u%% sub=%s (dirty=0x%02X)\n",
                  usage.fivePct, usage.sevenPct, usage.sub, dirty);
}

/* Parse a session line: "@S|sessions=2|busy=1". */
static void parseSessionLine(char *line) {
  Sessions next = sessions;
  char *save = nullptr;
  strtok_r(line, "|", &save);         // skip the "@S" prefix
  for (char *tok; (tok = strtok_r(nullptr, "|", &save)) != nullptr; ) {
    char *eq = strchr(tok, '=');
    if (!eq) continue;
    *eq = '\0';
    const char *key = tok, *val = eq + 1;
    if (!strcmp(key, "sessions"))  next.total = (uint8_t)constrain(atoi(val), 0, 99);
    else if (!strcmp(key, "busy")) next.busy  = (uint8_t)constrain(atoi(val), 0, 99);
  }
  next.valid = true;
  next.rxMs  = millis();

  bool changed = !sessions.valid || next.total != sessions.total ||
                 next.busy != sessions.busy;
  sessions = next;
  if (changed) {
    usageDirty |= UD_SESS;            // only the dot row needs a redraw
    Serial.printf("sessions: total=%u busy=%u\n", sessions.total, sessions.busy);
  }
}

/* Pull any complete lines out of the serial buffer (the host tool sends them). */
static void pumpSerial() {
  while (Serial.available()) {
    char c = (char)Serial.read();
    if (c == '\n' || c == '\r') {
      if (!serialDiscard && serialLen > 0) {
        serialLine[serialLen] = '\0';
        if (!strncmp(serialLine, "@U", 2))      parseUsageLine(serialLine);
        else if (!strncmp(serialLine, "@S", 2)) parseSessionLine(serialLine);
      }
      serialLen     = 0;             // newline always resyncs to a fresh line
      serialDiscard = false;
    } else if (serialDiscard) {
      // an overlong line is being dropped whole; swallow until the newline
    } else if (serialLen < sizeof(serialLine) - 1) {
      serialLine[serialLen++] = c;
    } else {
      serialDiscard = true;          // overlong: drop the WHOLE line, tail included,
      serialLen     = 0;             // rather than resyncing mid-line on its tail
    }
  }
}

void setup() {
  Serial.begin(115200);
  delay(300);
  Serial.println(F("\n\nusage_display " FIRMWARE_VERSION));

  panel.begin();
  panel.setTextWrap(false);   // overflowing text clips at the edge, never wraps

  // Boot splash: the Claude logo, large & centred, then the usage view.
  panel.fillScreen(ST77XX_BLACK);
  panel.blitPROGMEM((panel.width()  - CLAUDE_LOGO_W) / 2,
                    (panel.height() - CLAUDE_LOGO_H) / 2,
                    CLAUDE_LOGO_W, CLAUDE_LOGO_H, CLAUDE_LOGO);
  Serial.printf("panel: %dx%d\n", panel.width(), panel.height());
  delay(1500);

  usageDirty = UD_ALL;   // lay out the full usage view; shows "waiting for data"
  renderUsage();         // until the first @U/@S line arrives
}

void loop() {
  pumpSerial();          // ingest @U / @S lines from the host, every iteration

  static uint32_t lastFrame = 0;
  if (millis() - lastFrame >= 33) {   // ~30 fps; only animated regions redraw
    lastFrame = millis();
    tickResets();        // recompute reset countdowns (dirties a meter on change)
    tickSessionDecay();  // expire stale session dots (dirties the row on change)
    animateUsageFrame();
  }
}
