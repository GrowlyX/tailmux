#!/usr/bin/env python3
"""Renders docs/banner.svg: logo, wordmark and tagline on a gray card,
with the menu bar panel screenshot on the right.

Text is converted to outlines, so the SVG needs no font at view time and
the font file itself is never redistributed.

usage: scripts/banner.py --font Satoshi-Variable.woff2 [--panel docs/panel-dark.png] [--out docs/banner.svg]
needs: pip install fonttools brotli
"""

import argparse
import base64
import struct

from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen
from fontTools.ttLib import TTFont
from fontTools.varLib.instancer import instantiateVariableFont

W, H = 1280, 620
BG = "#2e3035"


def text_path(font_path, text, weight, size, x, baseline, tracking=0.0):
    """Returns (svg path data, advance width) for text set in the font."""
    font = instantiateVariableFont(TTFont(font_path), {"wght": weight})
    upm = font["head"].unitsPerEm
    scale = size / upm
    cmap, glyphs, hmtx = font.getBestCmap(), font.getGlyphSet(), font["hmtx"]
    pen = SVGPathPen(glyphs)
    cursor = x
    for ch in text:
        name = cmap[ord(ch)]
        glyphs[name].draw(TransformPen(pen, (scale, 0, 0, -scale, cursor, baseline)))
        cursor += hmtx[name][0] * scale + tracking
    return pen.getCommands(), cursor - x


def png_size(path):
    with open(path, "rb") as f:
        head = f.read(24)
    return struct.unpack(">II", head[16:24])


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--font", required=True)
    ap.add_argument("--panel", default="docs/panel-dark.png")
    ap.add_argument("--out", default="docs/banner.svg")
    a = ap.parse_args()

    # Panel screenshot, right side.
    pw, ph = png_size(a.panel)
    panel_h = 540
    panel_w = round(panel_h * pw / ph)
    panel_x, panel_y = W - 64 - panel_w, (H - panel_h) // 2
    with open(a.panel, "rb") as f:
        panel_b64 = base64.b64encode(f.read()).decode()

    # Left block: dot grid lit in Tailscale's "T", wordmark, tagline.
    left = 96
    mid = H // 2
    d, gap = 34, 16
    grid = 3 * d + 2 * gap
    gx, gy = left, mid - grid // 2 - 64
    lit = {0, 1, 2, 4, 7}
    dots = []
    for i in range(9):
        r, c = divmod(i, 3)
        cx = gx + c * (d + gap) + d / 2
        cy = gy + r * (d + gap) + d / 2
        op = "1" if i in lit else "0.28"
        dots.append(f'<circle cx="{cx:.1f}" cy="{cy:.1f}" r="{d / 2}" fill="#fff" fill-opacity="{op}"/>')

    word_base = gy + grid + 118
    tail, tail_w = text_path(a.font, "tail", 300, 108, left - 4, word_base, tracking=-1.5)
    mux, _ = text_path(a.font, "mux", 700, 108, left - 4 + tail_w, word_base, tracking=-1.5)
    tag, _ = text_path(a.font, "Be on all your tailnets at once.", 500, 30, left, word_base + 56)

    svg = f"""<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="{W}" height="{H}" viewBox="0 0 {W} {H}" role="img" aria-label="tailmux: be on all your tailnets at once">
  <defs>
    <clipPath id="card"><rect width="{W}" height="{H}" rx="28"/></clipPath>
    <clipPath id="panel"><rect x="{panel_x}" y="{panel_y}" width="{panel_w}" height="{panel_h}" rx="18"/></clipPath>
    <filter id="shadow" x="-20%" y="-20%" width="140%" height="140%">
      <feDropShadow dx="0" dy="18" stdDeviation="22" flood-color="#000" flood-opacity="0.45"/>
    </filter>
  </defs>
  <g clip-path="url(#card)">
    <rect width="{W}" height="{H}" fill="{BG}"/>
    {"".join(dots)}
    <path d="{tail}" fill="#fff"/>
    <path d="{mux}" fill="#fff"/>
    <path d="{tag}" fill="#aeb3bb"/>
    <rect x="{panel_x}" y="{panel_y}" width="{panel_w}" height="{panel_h}" rx="18" fill="{BG}" filter="url(#shadow)"/>
    <image x="{panel_x}" y="{panel_y}" width="{panel_w}" height="{panel_h}" clip-path="url(#panel)" preserveAspectRatio="xMidYMid slice" xlink:href="data:image/png;base64,{panel_b64}"/>
    <rect x="{panel_x + 0.5}" y="{panel_y + 0.5}" width="{panel_w - 1}" height="{panel_h - 1}" rx="18" fill="none" stroke="#fff" stroke-opacity="0.09"/>
  </g>
</svg>
"""
    with open(a.out, "w") as f:
        f.write(svg)
    print(f"wrote {a.out} ({len(svg) // 1024} KB)")


if __name__ == "__main__":
    main()
