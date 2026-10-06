#!/usr/bin/env python3
"""Renders macos/AppIcon.icns: the tailmux dots on a dark rounded square,
on Apple's 1024px icon grid (an 824px body with room for the shadow).

usage: macos/icon.py          needs: pip install pillow; iconutil (Xcode CLT)
"""

import os
import shutil
import subprocess
import tempfile

from PIL import Image, ImageDraw, ImageFilter

HERE = os.path.dirname(os.path.abspath(__file__))
SS = 4  # supersampling
# The tray icon's "T": rows top to bottom, columns left to right.
LIT = {0, 1, 2, 4, 7}


def squircle(size, inset, radius):
    """A rounded square mask with continuous-ish corners."""
    m = Image.new("L", (size * SS, size * SS), 0)
    ImageDraw.Draw(m).rounded_rectangle(
        [inset * SS, inset * SS, (size - inset) * SS, (size - inset) * SS], radius=radius * SS, fill=255)
    return m.resize((size, size), Image.LANCZOS)


def render(size=1024):
    inset, radius = 100, 185  # Big Sur grid: 824px body, ~22.5% corners
    body = size - 2 * inset

    # Background: a subtle top-to-bottom gradient in the banner's gray.
    bg = Image.new("RGBA", (size, size))
    top, bottom = (58, 61, 68), (34, 36, 41)
    px = bg.load()
    for y in range(size):
        t = min(1, max(0, (y - inset) / body))
        c = tuple(round(a + (b - a) * t) for a, b in zip(top, bottom))
        for x in range(size):
            px[x, y] = c + (255,)

    dots = Image.new("RGBA", (size * SS, size * SS), (0, 0, 0, 0))
    d = body * 0.17
    gap = body * 0.075
    origin = inset + (body - (3 * d + 2 * gap)) / 2
    draw = ImageDraw.Draw(dots)
    for i in range(9):
        row, col = divmod(i, 3)
        x, y = origin + col * (d + gap), origin + row * (d + gap)
        alpha = 255 if i in LIT else 80
        draw.ellipse([x * SS, y * SS, (x + d) * SS, (y + d) * SS], fill=(255, 255, 255, alpha))
    dots = dots.resize((size, size), Image.LANCZOS)

    mask = squircle(size, inset, radius)
    icon = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    # Soft drop shadow under the body, as macOS icons have.
    shadow = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    shadow.putalpha(mask.point(lambda v: v * 0.45))
    shadow = shadow.filter(ImageFilter.GaussianBlur(14))
    icon.alpha_composite(shadow, (0, 10))
    face = Image.alpha_composite(bg, dots)
    face.putalpha(mask)
    icon.alpha_composite(face)
    return icon


def main():
    big = render()
    tmp = tempfile.mkdtemp()
    iconset = os.path.join(tmp, "AppIcon.iconset")
    os.mkdir(iconset)
    for s in (16, 32, 128, 256, 512):
        big.resize((s, s), Image.LANCZOS).save(os.path.join(iconset, f"icon_{s}x{s}.png"))
        big.resize((2 * s, 2 * s), Image.LANCZOS).save(os.path.join(iconset, f"icon_{s}x{s}@2x.png"))
    out = os.path.join(HERE, "AppIcon.icns")
    subprocess.run(["iconutil", "-c", "icns", iconset, "-o", out], check=True)
    big.save(os.path.join(tmp, "AppIcon.png"))
    shutil.copy(os.path.join(tmp, "AppIcon.png"), "/tmp/AppIcon-preview.png")
    shutil.rmtree(tmp)
    print("wrote", out)


if __name__ == "__main__":
    main()
