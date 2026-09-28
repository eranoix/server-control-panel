#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODULE_DIR="$(dirname "$SCRIPT_DIR")"
ASSETS_DIR="$MODULE_DIR/src/androidTest/assets/hdiff"
WORK_DIR="$MODULE_DIR/.build/smoke"

HDIFFZ="${HDIFFZ:-$MODULE_DIR/.build/hdiffz}"
if [ ! -x "$HDIFFZ" ]; then
  HDIFFZ="$(command -v hdiffz || true)"
fi
if [ -z "$HDIFFZ" ] || [ ! -x "$HDIFFZ" ]; then
  cat >&2 <<'MSG'
make-smoke-fixtures: hdiffz not found.

The GENERATOR is deliberately not vendored in this module; only the patcher
ships on the device (see src/main/cpp/Android.mk). To build hdiffz locally:

  clone https://github.com/sisong/HDiffPatch (plus the siblings lzma/ zstd/ xxHash/
  libmd5/, at the commits in toolchain.properties), then inside it run:
      make LDEF=0 ZLIB=2 BSD=0 BZIP2=0 VCD=0 DIR_DIFF=0 -j8
  and point to it:  HDIFFZ=<path>/hdiffz ./tools/make-smoke-fixtures.sh

Or copy the binary to .build/hdiffz (ignored by version control).
MSG
  exit 1
fi

mkdir -p "$WORK_DIR" "$ASSETS_DIR"

python3 - "$WORK_DIR" <<'PY'
import os, random, sys

out = sys.argv[1]

# Fixed seed so the pair is byte-for-byte reproducible.
rnd = random.Random(20260906)
old = bytearray(rnd.getrandbits(8) for _ in range(256 * 1024))

# Pseudo-random on purpose: zeros would compress to nothing and the patch
# would not exercise the zstd decompressor.
new = bytearray(old)
for off in (0, 40000, 190000):
    # three local edits: covers at the start, middle and near the end
    new[off:off + 512] = bytes((i * 7 + 13) & 0xff for i in range(512))
# plus a new tail, so the new file differs in size from the old one
new += bytes(rnd.getrandbits(8) for _ in range(8 * 1024))

open(os.path.join(out, "smoke-old.bin"), "wb").write(bytes(old))
open(os.path.join(out, "smoke-new.bin"), "wb").write(bytes(new))
PY

"$HDIFFZ" -s-4m -c-zstd-21-24 -f \
  "$WORK_DIR/smoke-old.bin" "$WORK_DIR/smoke-new.bin" "$WORK_DIR/smoke.hdiff" >/dev/null

cp -f "$WORK_DIR/smoke-old.bin" "$ASSETS_DIR/smoke-old.bin"
cp -f "$WORK_DIR/smoke.hdiff"   "$ASSETS_DIR/smoke.hdiff"

echo "make-smoke-fixtures: written to $ASSETS_DIR" >&2
echo "  smoke-old.bin  $(stat -c%s "$ASSETS_DIR/smoke-old.bin") bytes  sha256=$(sha256sum "$ASSETS_DIR/smoke-old.bin" | cut -d' ' -f1)" >&2
echo "  smoke.hdiff    $(stat -c%s "$ASSETS_DIR/smoke.hdiff") bytes" >&2
echo >&2
echo "Expected SHA-256 of the NEW file (constant in ApkPatcherSmokeTest):" >&2
sha256sum "$WORK_DIR/smoke-new.bin" | cut -d' ' -f1 >&2
echo "size of the NEW file: $(stat -c%s "$WORK_DIR/smoke-new.bin")" >&2
