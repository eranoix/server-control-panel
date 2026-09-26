#!/usr/bin/env bash
# Regenerates internal/webassets/web/tailwind.css from the classes used in the
# front end. Run it (or `make tailwind`) after adding or changing Tailwind
# classes; the CSS is committed, so a plain build does not need it.
#
# The compiler is the pinned Tailwind v3 standalone binary, downloaded once
# into scripts/tailwindcss (git-ignored) and checked against its sha256.
set -euo pipefail

VERSION=3.4.17
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/scripts/tailwindcss"

case "$(uname -s)-$(uname -m)" in
  Linux-x86_64)  ASSET=tailwindcss-linux-x64;   SHA=7d24f7fa191d2193b78cd5f5a42a6093e14409521908529f42d80b11fde1f1d4 ;;
  Linux-aarch64) ASSET=tailwindcss-linux-arm64; SHA=69b1378b8133192d7d2feb12a116fa12d035594f58db3eff215879e4ad8cf39b ;;
  *) echo "✗ no pinned Tailwind binary for $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac

if ! echo "$SHA  $BIN" | sha256sum -c --quiet - 2>/dev/null; then
  echo "Downloading Tailwind $VERSION ($ASSET)"
  curl -fsSL -o "$BIN.tmp" "https://github.com/tailwindlabs/tailwindcss/releases/download/v$VERSION/$ASSET"
  echo "$SHA  $BIN.tmp" | sha256sum -c --quiet - || { rm -f "$BIN.tmp"; echo "✗ checksum mismatch" >&2; exit 1; }
  chmod +x "$BIN.tmp" && mv "$BIN.tmp" "$BIN"
fi

cd "$ROOT"
out=internal/webassets/web/tailwind.css
"$BIN" -c scripts/tailwind/tailwind.config.js -i scripts/tailwind/input.css -o "$out" --minify
echo "OK: $out regenerated ($(stat -c%s "$out") bytes)"
