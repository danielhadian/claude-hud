#!/usr/bin/env python3
"""Faithful host-side preview of the 128x160 ST7735 usage panel.

Renders with the ACTUAL Adafruit_GFX glyph data (FreeSans* custom fonts + the
built-in 5x7 glcdfont) and the same draw primitives the firmware uses, so text
sizes, right-alignment and overlaps show up truthfully — no more guessing blind.

Edit LAYOUT / DATA below and run:  python3 panel_preview.py out.png [scale]
"""
import os
import re
import sys

from PIL import Image, ImageDraw

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from svg_path_to_rgb565 import render_svg_image

GFXDIR = os.path.expanduser("~/Arduino/libraries/Adafruit_GFX_Library")
W, H = 128, 160

# firmware colour constants (approx RGB of the RGB565 values)
BLACK = (0, 0, 0)
BRAND = (0xD9, 0x77, 0x57)   # PANEL_BRAND
DIM   = (0x8A, 0x84, 0x7E)   # COL_DIM  (warm grey)
FIG   = (0xF4, 0xEA, 0xE4)   # COL_FIG  (warm white)
TRACK = (0x1A, 0x1A, 0x1C)   # COL_TRACK
EDGE  = (0x39, 0x39, 0x3E)   # COL_EDGE
CREST = (0xFF, 0xE4, 0xD8)   # COL_CREST


# ---- font parsing --------------------------------------------------------

def parse_gfxfont(name):
    txt = open(f"{GFXDIR}/Fonts/{name}.h").read()
    bm = re.search(rf'{name}Bitmaps\[\]\s*PROGMEM\s*=\s*{{(.*?)}};', txt, re.S).group(1)
    bitmap = [int(x, 16) for x in re.findall(r'0x[0-9A-Fa-f]+', bm)]
    gl = re.search(rf'{name}Glyphs\[\]\s*PROGMEM\s*=\s*{{(.*?)}};', txt, re.S).group(1)
    glyphs = [tuple(int(v) for v in m.groups())
              for m in re.finditer(
                  r'\{\s*(-?\d+),\s*(-?\d+),\s*(-?\d+),\s*(-?\d+),\s*(-?\d+),\s*(-?\d+)\s*\}', gl)]
    fsblk = re.search(rf'{name}\s*PROGMEM\s*=\s*{{(.*?)}};', txt, re.S).group(1)
    m = re.search(r'0x([0-9A-Fa-f]+),\s*0x([0-9A-Fa-f]+),\s*(\d+)', fsblk)
    return dict(bitmap=bitmap, glyphs=glyphs,
                first=int(m.group(1), 16), last=int(m.group(2), 16), yadv=int(m.group(3)))


def parse_glcd():
    txt = open(f"{GFXDIR}/glcdfont.c").read()
    body = txt.split("font[]", 1)[1]
    return [int(x, 16) for x in re.findall(r'0x[0-9A-Fa-f]+', body)][:256 * 5]


GLCD = parse_glcd()


# ---- canvas --------------------------------------------------------------

class Canvas:
    def __init__(self):
        self.img = Image.new("RGB", (W, H), BLACK)
        self.px = self.img.load()
        self.d = ImageDraw.Draw(self.img)

    def _set(self, x, y, c):
        if 0 <= x < W and 0 <= y < H:
            self.px[int(x), int(y)] = c

    def fill_rect(self, x, y, w, h, c):
        if w > 0 and h > 0:
            self.d.rectangle([x, y, x + w - 1, y + h - 1], fill=c)

    def round_rect(self, x, y, w, h, r, c, fill=False):
        box = [x, y, x + w - 1, y + h - 1]
        if fill:
            self.d.rounded_rectangle(box, radius=r, fill=c)
        else:
            self.d.rounded_rectangle(box, radius=r, outline=c)

    def fill_circle(self, cx, cy, r, c):
        self.d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=c)

    def draw_circle(self, cx, cy, r, c):
        self.d.ellipse([cx - r, cy - r, cx + r, cy + r], outline=c)

    def hline(self, x, y, w, c):
        self.d.line([x, y, x + w - 1, y], fill=c)

    def blit(self, x, y, pil_img):
        self.img.paste(pil_img, (int(x), int(y)))

    # -- built-in 5x7 (classic) font: cursor y = glyph TOP, 6px advance --
    def classic_char(self, x, y, ch, c):
        o = ord(ch) * 5
        for col in range(5):
            bits = GLCD[o + col]
            for row in range(8):
                if bits & (1 << row):
                    self._set(x + col, y + row, c)
        return 6

    def classic_text(self, x, y, s, c):
        for ch in s:
            x += self.classic_char(x, y, ch, c)
        return x

    @staticmethod
    def classic_width(s):
        return len(s) * 6 - 1

    # -- GFX custom font: cursor y = BASELINE --
    def gfx_char(self, x, y, ch, font, c):
        code = ord(ch)
        if code < font['first'] or code > font['last']:
            return 0
        off, gw, gh, xa, dx, dy = font['glyphs'][code - font['first']]
        bo, bitpos, byte = off, 0, 0
        for yy in range(gh):
            for xx in range(gw):
                if bitpos == 0:
                    byte = font['bitmap'][bo]; bo += 1; bitpos = 8
                bitpos -= 1
                if byte & (1 << bitpos):
                    self._set(x + dx + xx, y + dy + yy, c)
        return xa

    def gfx_text(self, x, y, s, font, c):
        for ch in s:
            x += self.gfx_char(x, y, ch, font, c)
        return x

    @staticmethod
    def gfx_bounds(s, font):
        """Mimic Adafruit getTextBounds -> (w, h). Baseline-relative."""
        x = 0
        minx = miny = 1 << 30
        maxx = maxy = -(1 << 30)
        for ch in s:
            code = ord(ch)
            if code < font['first'] or code > font['last']:
                continue
            _, gw, gh, xa, dx, dy = font['glyphs'][code - font['first']]
            if gw and gh:
                x1, y1 = x + dx, dy
                x2, y2 = x1 + gw - 1, y1 + gh - 1
                minx, miny = min(minx, x1), min(miny, y1)
                maxx, maxy = max(maxx, x2), max(maxy, y2)
            x += xa
        if maxx < minx:
            return 0, 0
        return maxx - minx + 1, maxy - miny + 1

    def save(self, path, scale=3):
        self.img.resize((W * scale, H * scale), Image.NEAREST).save(path)


def load_rgb565_h(path):
    """Parse a big-endian RGB565 PROGMEM header (svg2rgb565 output) -> PIL image."""
    txt = open(path).read()
    w = int(re.search(r'_W\s+(\d+)', txt).group(1))
    h = int(re.search(r'_H\s+(\d+)', txt).group(1))
    body = txt.split("PROGMEM", 1)[1]
    b = [int(x, 16) for x in re.findall(r'0x[0-9A-Fa-f]+', body)]
    img = Image.new("RGB", (w, h))
    px = img.load()
    for i in range(w * h):
        word = (b[2*i] << 8) | b[2*i+1]
        r5, g6, b5 = word >> 11, (word >> 5) & 0x3F, word & 0x1F
        px[i % w, i // w] = (r5 << 3 | r5 >> 2, g6 << 2 | g6 >> 4, b5 << 3 | b5 >> 2)
    return img


BOLD12 = parse_gfxfont("FreeSansBold12pt7b")
BOLD9 = parse_gfxfont("FreeSansBold9pt7b")
SANS9 = parse_gfxfont("FreeSans9pt7b")

CLAUDE_SVG = open(os.path.join(os.path.dirname(os.path.abspath(__file__)),
                               "claude-logo.svg")).read()


# ==========================================================================
# SCENE: the CURRENT shipped layout, for validating the engine is faithful.
# ==========================================================================

def scene_current(cv, five=45, seven=64, tier="Max 20x", sessions=2, busy=2):
    logo = render_svg_image(CLAUDE_SVG, 20)
    cv.blit(6, 3, logo)                                    # header mark (current: 20px)

    # tier pill (built-in, right-aligned, sized to text)
    tw = cv.classic_width(tier)
    pillW = tw + 12
    px = W - 8 - pillW
    cv.round_rect(px, 4, pillW, 14, 7, BRAND)
    cv.classic_text(px + 6, 4 + (14 - 7) // 2 + 1, tier, BRAND)

    def bar(labelY, trackY, resetY, label, pct, reset):
        # label: built-in, at labelY-12
        cv.classic_text(8, labelY - 12, label, DIM)
        # big %: FreeSansBold12pt, right-aligned to x=120
        num = str(pct)
        wP, _ = cv.gfx_bounds("%", BOLD12)
        wN, _ = cv.gfx_bounds(num, BOLD12)
        rightX = 8 + 112
        cv.gfx_text(rightX - wP - wN - 3, labelY, num, BOLD12, FIG)
        cv.gfx_text(rightX - wP, labelY, "%", BOLD12, BRAND)
        # track + fill
        cv.round_rect(8, trackY, 112, 11, 5, EDGE)
        cv.round_rect(9, trackY + 1, 110, 9, 4, TRACK, fill=True)
        fillW = pct * 110 // 100
        if fillW > 0:
            cv.round_rect(9, trackY + 1, fillW, 9, 4, BRAND, fill=True)
            if fillW > 3:
                cv.hline(10, trackY + 2, fillW - 2, CREST)
        cv.classic_text(8, resetY, f"resets in {reset}", DIM)

    bar(46, 52, 71, "5-HOUR", five, "52m")
    bar(96, 102, 122, "WEEKLY", seven, "3d 4h")

    cv.hline(8, 138, 112, TRACK)
    idle = max(0, sessions - busy)
    shown = min(sessions, 3)
    cx = 8 + 3
    for i in range(shown):
        (cv.fill_circle if i < busy else cv.draw_circle)(cx, 150, 3, BRAND if i < busy else DIM)
        cx += 8
    tx = 8 + shown * 8 + 6
    tx = cv.classic_text(tx, 150 - 3, str(busy), BRAND)
    tx = cv.classic_text(tx, 150 - 3, " active ", DIM)
    tx = cv.classic_text(tx, 150 - 3, str(idle), FIG)
    cv.classic_text(tx, 150 - 3, " idle", DIM)


# ==========================================================================
# SCENE: the NEW proposed layout — bigger logo, 9pt numbers, even spacing.
# ==========================================================================

HDR_H = 30   # header band height

def scene_new(cv, five=45, seven=64, tier="Max 20x", sessions=2, busy=2,
              mascot=None):
    # header: the MASCOT (unchanged) top-left, tier pill right, both vertically
    # centred in the header band. The Claude logo is boot-only, never here.
    if mascot is not None:
        my = 3 + (HDR_H - mascot.height) // 2
        cv.blit(5, my, mascot)
    tw = cv.classic_width(tier)
    pillW, pillH, pillY = tw + 12, 15, 3 + (HDR_H - 15) // 2
    px = W - 6 - pillW
    cv.round_rect(px, pillY, pillW, pillH, pillH // 2, BRAND)
    cv.classic_text(px + 6, pillY + (pillH - 7) // 2 + 1, tier, BRAND)

    def bar(base, trackY, resetTop, label, pct, reset):
        # small built-in caption (dim) + bold 9pt number (the value pops)
        cv.classic_text(8, base - 6, label, DIM)
        num = str(pct)
        wP, _ = cv.gfx_bounds("%", BOLD9)
        wN, _ = cv.gfx_bounds(num, BOLD9)
        rightX = 8 + 112
        cv.gfx_text(rightX - wP - wN - 2, base, num, BOLD9, FIG)
        cv.gfx_text(rightX - wP, base, "%", BOLD9, BRAND)
        # track + coral fill
        cv.round_rect(8, trackY, 112, 10, 5, EDGE)
        cv.round_rect(9, trackY + 1, 110, 8, 4, TRACK, fill=True)
        fillW = pct * 110 // 100
        if fillW > 0:
            cv.round_rect(9, trackY + 1, fillW, 8, 4, BRAND, fill=True)
            if fillW > 3:
                cv.hline(10, trackY + 2, fillW - 2, CREST)
        cv.classic_text(8, resetTop, f"resets {reset}", DIM)

    bar(50, 58, 70, "5-HOUR", five, "52m")
    bar(96, 104, 116, "WEEKLY", seven, "3d 4h")

    # sessions: a small caption + ONE dot per session, accurate.
    # busy = filled coral (these pulse on the panel); idle = hollow grey.
    cv.hline(8, 132, 112, TRACK)
    cv.classic_text(8, 145, "SESSIONS", DIM)
    idle = max(0, sessions - busy)
    x0 = 8 + cv.classic_width("SESSIONS") + 8
    CAP = 7                                     # dots that fit right of the caption
    shown = min(sessions, CAP)
    cx = x0 + 3
    for i in range(shown):
        if i < busy:
            cv.fill_circle(cx, 148, 3, BRAND)
        else:
            cv.draw_circle(cx, 148, 3, DIM)
        cx += 9
    if sessions == 0:
        cv.classic_text(x0, 145, "none", DIM)
    elif sessions > CAP:
        cv.classic_text(cx - 1, 145, "+", DIM)


def scene_boot(cv):
    """Boot splash: the Claude logo, large, centred."""
    sz = 96
    logo = render_svg_image(CLAUDE_SVG, sz)
    cv.blit((W - sz) // 2, (H - sz) // 2, logo)


if __name__ == "__main__":
    scene = sys.argv[1] if len(sys.argv) > 1 else "new"
    out = sys.argv[2] if len(sys.argv) > 2 else "/tmp/panel.png"
    scale = int(sys.argv[3]) if len(sys.argv) > 3 else 3
    default_mascot = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                  "..", "usage_display", "mascot.h")
    mascot = load_rgb565_h(os.environ.get("MASCOT_H", default_mascot))
    cv = Canvas()
    if scene == "current":
        scene_current(cv)
    elif scene == "boot":
        scene_boot(cv)
    elif scene == "new100":
        scene_new(cv, five=100, seven=100, sessions=3, busy=1, mascot=mascot)
    else:
        s = int(os.environ.get("SESS", 2))
        b = int(os.environ.get("BUSY", 2))
        scene_new(cv, sessions=s, busy=b, mascot=mascot)
    cv.save(out, scale)
    print(f"wrote {out} @ {scale}x")
