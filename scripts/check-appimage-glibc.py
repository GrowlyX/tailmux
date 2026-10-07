#!/usr/bin/env python3
"""Reject AppImages whose executables or bundled libraries need glibc > 2.35."""

import argparse
import re
import subprocess
import tempfile
from pathlib import Path


def check(appimage: Path) -> None:
    baseline = (2, 35)
    with tempfile.TemporaryDirectory(prefix="tailmux-appimage-") as directory:
        subprocess.run(
            [str(appimage.resolve()), "--appimage-extract"],
            cwd=directory,
            stdout=subprocess.DEVNULL,
            check=True,
        )
        root = Path(directory) / "squashfs-root"
        if not (root / "AppRun").is_file():
            raise SystemExit("AppImage extraction did not produce AppRun")
        failures = []
        elf_count = 0
        # Check the runtime too, along with the app, Go sidecar and all libraries.
        for binary in [appimage, *root.rglob("*")]:
            if not binary.is_file() or binary.is_symlink():
                continue
            with binary.open("rb") as stream:
                if stream.read(4) != b"\x7fELF":
                    continue
            elf_count += 1
            symbols = subprocess.run(
                ["readelf", "--dyn-syms", "--wide", str(binary)],
                capture_output=True,
                text=True,
                check=True,
            ).stdout
            # Only undefined symbols require a version from another library;
            # exported version definitions are not runtime requirements.
            versions = re.findall(r"\bUND\b.*?@GLIBC_(\d+(?:\.\d+)+)\b", symbols)
            required = max((tuple(map(int, v.split("."))) for v in versions), default=(0,))
            if required > baseline:
                label = binary.relative_to(root) if binary != appimage else appimage.name
                failures.append(f"{label}: requires GLIBC_{'.'.join(map(str, required))}")
        if not elf_count:
            raise SystemExit("No ELF binaries found in AppImage")
        if failures:
            raise SystemExit("AppImage exceeds the glibc 2.35 baseline:\n" + "\n".join(failures))
        print(f"{appimage.name}: checked {elf_count} ELF files; glibc <= 2.35")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("appimage", type=Path)
    check(parser.parse_args().appimage)
