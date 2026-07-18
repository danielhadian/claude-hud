#!/usr/bin/env python3
"""Rasterise an SVG <path> (curves + even-odd holes) to an RGB565 PROGMEM blob.

Unlike svg2rgb565.py (axis-aligned only), this uses pycairo to fill an arbitrary
path with the even-odd rule, supersamples for anti-aliasing, pre-blends against
black, and emits big-endian RGB565 — the ST7735's native wire format.

    python3 svg_path_to_rgb565.py --svg claude-logo.svg --size 100 \
        --name CLAUDE_LOGO --out logo_boot.h --png /tmp/logo.png

Also writes a scaled-up PNG (--png) so the shape can be eyeballed before flashing.
"""
import argparse
import re
import sys

import cairo
from PIL import Image

TOKEN = re.compile(r"([MmLlHhVvCcSsQqTtAaZz])|(-?\d*\.?\d+(?:[eE][-+]?\d+)?)")


def tokenize(d):
    for m in TOKEN.finditer(d):
        yield ("cmd", m.group(1)) if m.group(1) else ("num", float(m.group(2)))


def build_path(ctx, d):
    """Feed an SVG path 'd' into a cairo context (absolute + relative, beziers)."""
    toks = list(tokenize(d))
    i, n = 0, len(toks)
    cx = cy = 0.0            # current point
    sx = sy = 0.0            # subpath start
    pcx = pcy = None         # previous cubic control (for S/s)
    pqx = pqy = None         # previous quad control (for T/t)
    cmd = None

    def num():
        nonlocal i
        v = toks[i][1]; i += 1; return v

    while i < n:
        if toks[i][0] == "cmd":
            cmd = toks[i][1]; i += 1
            if cmd in "Zz":
                ctx.close_path(); cx, cy = sx, sy; pcx = pqx = None
                continue
        # implicit repeat: after M/m subsequent pairs are L/l
        c = cmd
        if c in "Mm":
            x, y = num(), num()
            if c == "m": x, y = cx + x, cy + y
            ctx.move_to(x, y); cx, cy = x, y; sx, sy = x, y
            cmd = "l" if c == "m" else "L"; pcx = pqx = None
        elif c in "Ll":
            x, y = num(), num()
            if c == "l": x, y = cx + x, cy + y
            ctx.line_to(x, y); cx, cy = x, y; pcx = pqx = None
        elif c in "Hh":
            x = num(); x = cx + x if c == "h" else x
            ctx.line_to(x, cy); cx = x; pcx = pqx = None
        elif c in "Vv":
            y = num(); y = cy + y if c == "v" else y
            ctx.line_to(cx, y); cy = y; pcx = pqx = None
        elif c in "Cc":
            x1, y1, x2, y2, x, y = (num() for _ in range(6))
            if c == "c":
                x1, y1, x2, y2, x, y = cx+x1, cy+y1, cx+x2, cy+y2, cx+x, cy+y
            ctx.curve_to(x1, y1, x2, y2, x, y)
            pcx, pcy = x2, y2; cx, cy = x, y; pqx = None
        elif c in "Ss":
            x2, y2, x, y = (num() for _ in range(4))
            if c == "s": x2, y2, x, y = cx+x2, cy+y2, cx+x, cy+y
            x1, y1 = (2*cx - pcx, 2*cy - pcy) if pcx is not None else (cx, cy)
            ctx.curve_to(x1, y1, x2, y2, x, y)
            pcx, pcy = x2, y2; cx, cy = x, y; pqx = None
        elif c in "Qq":
            qx, qy, x, y = (num() for _ in range(4))
            if c == "q": qx, qy, x, y = cx+qx, cy+qy, cx+x, cy+y
            # quad -> cubic
            ctx.curve_to(cx+2/3*(qx-cx), cy+2/3*(qy-cy),
                         x+2/3*(qx-x), y+2/3*(qy-y), x, y)
            pqx, pqy = qx, qy; cx, cy = x, y; pcx = None
        elif c in "Tt":
            x, y = num(), num()
            if c == "t": x, y = cx + x, cy + y
            qx, qy = (2*cx - pqx, 2*cy - pqy) if pqx is not None else (cx, cy)
            ctx.curve_to(cx+2/3*(qx-cx), cy+2/3*(qy-cy),
                         x+2/3*(qx-x), y+2/3*(qy-y), x, y)
            pqx, pqy = qx, qy; cx, cy = x, y; pcx = None
        else:
            raise ValueError(f"unsupported path command {c!r}")


def render_svg_image(svg, size, fg="D97757", ss=4):
    """Rasterise SVG text -> a `size`x`size` PIL RGB image, coral over black."""
    vb = re.search(r'viewBox="([\d.\-\s]+)"', svg)
    _, _, vw, vh = (float(x) for x in vb.group(1).split()) if vb else (0, 0, 24, 24)
    d = re.search(r'\bd="([^"]+)"', svg).group(1)
    r, g, b = (int(fg[i:i+2], 16) / 255 for i in (0, 2, 4))

    S = size * ss
    surf = cairo.ImageSurface(cairo.FORMAT_ARGB32, S, S)
    ctx = cairo.Context(surf)
    scale = S / max(vw, vh)                      # fit viewBox, centered, keep aspect
    ctx.translate((S - vw*scale)/2, (S - vh*scale)/2)
    ctx.scale(scale, scale)
    ctx.set_fill_rule(cairo.FILL_RULE_EVEN_ODD)
    ctx.set_source_rgb(r, g, b)
    build_path(ctx, d)
    ctx.fill()
    surf.flush()

    # ARGB32 premultiplied, little-endian byte order = B,G,R,A. Over black, the
    # premultiplied RGB *is* the composited result.
    stride = surf.get_stride()
    raw = bytes(surf.get_data())
    big = Image.new("RGB", (S, S))
    px = big.load()
    for y in range(S):
        row = y * stride
        for x in range(S):
            o = row + x*4
            px[x, y] = (raw[o+2], raw[o+1], raw[o])
    return big.resize((size, size), Image.LANCZOS)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--svg", required=True)
    ap.add_argument("--size", type=int, default=100, help="square output px")
    ap.add_argument("--name", default="LOGO")
    ap.add_argument("--out", help="RGB565 .h to write")
    ap.add_argument("--png", help="also write a scaled PNG preview")
    ap.add_argument("--fg", default="D97757")
    ap.add_argument("--ss", type=int, default=4, help="supersample factor")
    args = ap.parse_args()

    small = render_svg_image(open(args.svg).read(), args.size, args.fg, args.ss)

    if args.png:
        small.resize((args.size*4, args.size*4), Image.NEAREST).save(args.png)
        print(f"wrote preview {args.png}", file=sys.stderr)

    if args.out:
        sp = small.load()
        with open(args.out, "w") as f:
            f.write(f"// Generated by svg_path_to_rgb565.py --name {args.name} "
                    f"--size {args.size}.\n")
            f.write("// RGB565, big-endian, pre-blended against black. Do not edit by hand.\n")
            f.write("#pragma once\n#include <Arduino.h>\n\n")
            f.write(f"#define {args.name}_W {args.size}\n")
            f.write(f"#define {args.name}_H {args.size}\n\n")
            f.write(f"const uint8_t {args.name}[{args.size*args.size*2}] PROGMEM = {{\n")
            for y in range(args.size):
                line = []
                for x in range(args.size):
                    rr, gg, bb = sp[x, y]
                    w = ((rr & 0xF8) << 8) | ((gg & 0xFC) << 3) | (bb >> 3)
                    line += [(w >> 8) & 0xFF, w & 0xFF]
                for k in range(0, len(line), 16):
                    f.write("  " + "".join(f"0x{v:02X}," for v in line[k:k+16]) + "\n")
            f.write("};\n")
        print(f"wrote {args.out}: {args.size*args.size*2} bytes ({args.name})",
              file=sys.stderr)


if __name__ == "__main__":
    main()
