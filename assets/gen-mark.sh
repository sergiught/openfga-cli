#!/usr/bin/env sh
# Regenerates internal/ui/logo/mark_22.png from assets/openfga_logo.svg.
# Dev-only tools: rsvg-convert (librsvg) and magick (ImageMagick 7).
# The mark is the leftmost 278x278 region of the full 1161x278 lockup.
set -e
cd "$(dirname "$0")/.."
tmp=$(mktemp -t ofga_logo_XXXX.png)
rsvg-convert -w 1161 assets/openfga_logo.svg -o "$tmp"
magick "$tmp" -crop 278x278+0+0 +repage -filter point -resize 22x22 png32:internal/ui/logo/mark_22.png
rm -f "$tmp"
echo "wrote internal/ui/logo/mark_22.png"
