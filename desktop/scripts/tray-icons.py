#!/usr/bin/env python3
"""Renders the tray icons: Tailscale's 3x3 dot grid with N dots lit.

Dots light in the order that draws Tailscale's "T" (top row, then down
the middle), matching macos/TailmuxBar/Sources/TailmuxBar/Icon.swift.
Pure Python (no Pillow) so it runs anywhere; circles are supersampled.

    python3 desktop/scripts/tray-icons.py            # tray PNGs
    python3 desktop/scripts/tray-icons.py --app      # plus the 1024px app icon

Writes desktop/src-tauri/icons/tray/{light,dark}-{0..9,off}.png and, with
--app, desktop/src-tauri/icons/app-source.png for `pnpm tauri icon`.
"""
import os
import struct
import sys
import zlib

FILL_ORDER = [0, 1, 2, 4, 7, 3, 5, 6, 8]
HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "..", "src-tauri", "icons")


def png(width, height, rgba_rows):
    raw = b"".join(b"\x00" + bytes(row) for row in rgba_rows)

    def chunk(tag, data):
        c = tag + data
        return struct.pack(">I", len(data)) + c + struct.pack(">I", zlib.crc32(c) & 0xFFFFFFFF)

    return (b"\x89PNG\r\n\x1a\n"
            + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(raw, 9))
            + chunk(b"IEND", b""))


def render(size, lit, reachable, rgb, background=None, ss=4):
    """A size x size grid of dots. `lit` dots at full alpha, the rest at
    30%, or everything at 18% when the daemon is unreachable."""
    d = size * 0.235
    gap = size * 0.1
    origin = (size - (3 * d + 2 * gap)) / 2
    lit_set = set(FILL_ORDER[:max(0, min(lit, 9))])
    dots = []
    for i in range(9):
        row, col = divmod(i, 3)
        cx = origin + col * (d + gap) + d / 2
        cy = origin + row * (d + gap) + d / 2
        alpha = 0.18 if not reachable else (1.0 if i in lit_set else 0.3)
        dots.append((cx, cy, d / 2, alpha))
    rows = []
    for y in range(size):
        row = []
        for x in range(size):
            cover = 0.0
            for sy in range(ss):
                for sx in range(ss):
                    px = x + (sx + 0.5) / ss
                    py = y + (sy + 0.5) / ss
                    for cx, cy, r, a in dots:
                        if (px - cx) ** 2 + (py - cy) ** 2 <= r * r:
                            cover += a
                            break
            a = cover / (ss * ss)
            if background is None:
                row += [rgb[0], rgb[1], rgb[2], round(a * 255)]
            else:
                # Composite onto an opaque background (app icon).
                row += [round(rgb[c] * a + background[c] * (1 - a)) for c in range(3)] + [255]
        rows.append(row)
    return png(size, size, rows)


def main():
    os.makedirs(os.path.join(OUT, "tray"), exist_ok=True)
    variants = {"light": (255, 255, 255), "dark": (30, 30, 30)}
    for name, rgb in variants.items():
        for lit in range(10):
            with open(os.path.join(OUT, "tray", f"{name}-{lit}.png"), "wb") as f:
                f.write(render(32, lit, True, rgb))
        with open(os.path.join(OUT, "tray", f"{name}-off.png"), "wb") as f:
            f.write(render(32, 0, False, rgb))
    if "--app" in sys.argv:
        # Five lit dots spell the "T"; on the app icon that reads as the logo.
        with open(os.path.join(OUT, "app-source.png"), "wb") as f:
            f.write(render(1024, 5, True, (255, 255, 255), background=(28, 30, 36), ss=3))
    print("wrote", os.path.join(OUT, "tray"))


if __name__ == "__main__":
    main()
