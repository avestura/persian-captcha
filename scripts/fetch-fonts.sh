#!/usr/bin/env bash
#
# Downloads the Vazirmatn variable font into web/assets/fonts, where go:embed
# picks it up and the service serves it from /v1/font/.
#
# The font is not committed to this repository. Vendoring a typeface into
# source control is a licensing decision each operator should make
# deliberately: the OFL requires the licence to travel with the font, and this
# directory is compiled into the service binary.
#
# Without it, Persian text still renders correctly using the system font stack
# declared in internal/i18n/locales/fa.json. The bundled font only makes the
# result consistent across machines.

set -euo pipefail

VERSION="${VAZIRMATN_VERSION:-33.003}"
DEST="$(cd "$(dirname "$0")/.." && pwd)/web/assets/fonts"
BASE="https://github.com/rastikerdar/vazirmatn/releases/download/v${VERSION}"

mkdir -p "$DEST"

fetch() {
  local url="$1" out="$2"
  echo "fetching $url"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 3 -o "$out" "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$out" "$url"
  else
    echo "need curl or wget" >&2
    exit 1
  fi
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fetch "${BASE}/vazirmatn-v${VERSION}.zip" "$tmp/vazirmatn.zip"
unzip -q -o "$tmp/vazirmatn.zip" -d "$tmp/unpacked"

# Only the variable WOFF2 and the licence are wanted; the release archive also
# carries static weights in several formats, which would bloat the binary.
find "$tmp/unpacked" -name 'Vazirmatn[wght].woff2' -exec cp {} "$DEST/Vazirmatn.woff2" \;
find "$tmp/unpacked" -iname 'OFL.txt' -exec cp {} "$DEST/OFL.txt" \;

if [ ! -f "$DEST/Vazirmatn.woff2" ]; then
  echo "the archive did not contain the variable WOFF2; check the release layout" >&2
  exit 1
fi

echo "installed:"
ls -lh "$DEST"/Vazirmatn.woff2 "$DEST"/OFL.txt
echo
echo "rebuild the service for it to take effect: go build ./cmd/captchad"
