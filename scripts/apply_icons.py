#!/usr/bin/env python3
"""Generate web + Flutter app icons from icon/anvil.ico."""

from __future__ import annotations

from pathlib import Path

from PIL import Image

ROOT = Path(__file__).resolve().parents[1]
SRC = ROOT / "icon" / "anvil.ico"


def load_base() -> Image.Image:
    im = Image.open(SRC)
    # Prefer largest frame if multi-size ICO.
    best = im.convert("RGBA")
    n = getattr(im, "n_frames", 1)
    for i in range(n):
        im.seek(i)
        frame = im.convert("RGBA")
        if frame.width * frame.height > best.width * best.height:
            best = frame
    return best


def resize(src: Image.Image, size: int) -> Image.Image:
    return src.resize((size, size), Image.Resampling.LANCZOS)


def save_ico(src: Image.Image, dest: Path, sizes: list[int]) -> None:
    dest.parent.mkdir(parents=True, exist_ok=True)
    # Pillow writes multi-size ICO from the largest image + sizes=.
    largest = max(sizes)
    resize(src, largest).save(
        dest,
        format="ICO",
        sizes=[(s, s) for s in sizes],
    )


def save_png(src: Image.Image, dest: Path, size: int) -> None:
    dest.parent.mkdir(parents=True, exist_ok=True)
    resize(src, size).save(dest, format="PNG")


def main() -> None:
    if not SRC.is_file():
        raise SystemExit(f"missing source icon: {SRC}")
    base = load_base()
    print(f"source: {SRC} ({base.width}x{base.height})")

    # Web favicon
    web_static = ROOT / "source" / "internal" / "web" / "static"
    save_ico(base, web_static / "favicon.ico", [16, 32, 48, 64, 128, 256])
    save_png(base, web_static / "favicon-32.png", 32)
    save_png(base, web_static / "apple-touch-icon.png", 180)
    print("web: favicon.ico, favicon-32.png, apple-touch-icon.png")

    # Windows Flutter runner
    win_ico = ROOT / "app" / "windows" / "runner" / "resources" / "app_icon.ico"
    save_ico(base, win_ico, [16, 32, 48, 64, 128, 256])
    print(f"windows: {win_ico.relative_to(ROOT)}")

    # Android mipmaps
    android = {
        "mipmap-mdpi": 48,
        "mipmap-hdpi": 72,
        "mipmap-xhdpi": 96,
        "mipmap-xxhdpi": 144,
        "mipmap-xxxhdpi": 192,
    }
    res = ROOT / "app" / "android" / "app" / "src" / "main" / "res"
    for folder, size in android.items():
        save_png(base, res / folder / "ic_launcher.png", size)
    print("android: mipmap ic_launcher.png sizes written")

    # iOS AppIcon set (pixel size = point * scale)
    ios_dir = (
        ROOT
        / "app"
        / "ios"
        / "Runner"
        / "Assets.xcassets"
        / "AppIcon.appiconset"
    )
    ios_files = {
        "Icon-App-20x20@1x.png": 20,
        "Icon-App-20x20@2x.png": 40,
        "Icon-App-20x20@3x.png": 60,
        "Icon-App-29x29@1x.png": 29,
        "Icon-App-29x29@2x.png": 58,
        "Icon-App-29x29@3x.png": 87,
        "Icon-App-40x40@1x.png": 40,
        "Icon-App-40x40@2x.png": 80,
        "Icon-App-40x40@3x.png": 120,
        "Icon-App-60x60@2x.png": 120,
        "Icon-App-60x60@3x.png": 180,
        "Icon-App-76x76@1x.png": 76,
        "Icon-App-76x76@2x.png": 152,
        "Icon-App-83.5x83.5@2x.png": 167,
        "Icon-App-1024x1024@1x.png": 1024,
    }
    for name, size in ios_files.items():
        save_png(base, ios_dir / name, size)
    print("ios: AppIcon.appiconset updated")
    print("done")


if __name__ == "__main__":
    main()
