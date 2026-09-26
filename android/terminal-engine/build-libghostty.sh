#!/usr/bin/env bash
# Cross-compiles libghostty-vt.a for arm64-v8a and x86_64 from the pinned
# Ghostty commit in toolchain.properties, and vendors the result.
#
# Builds from source deliberately (never downloads a prebuilt .a): we control
# the compiler, the source commit and the flags, so an unaudited third-party
# binary never enters the build (04-PLAN.md threat T-04-SC).
#
# Idempotent and re-runnable: re-running reproduces identical checksums
# because the source is a pinned commit and the flags are fixed here.
#
# Usage: ./build-libghostty.sh   (from anywhere; paths are resolved from this
#                                  script's own location)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# shellcheck source=./toolchain-env.sh
. ./toolchain-env.sh

PROPS_FILE="$SCRIPT_DIR/toolchain.properties"
prop() { grep -E "^$1=" "$PROPS_FILE" | tail -1 | cut -d= -f2-; }

GHOSTTY_COMMIT="$(prop GHOSTTY_COMMIT)"
GHOSTTY_REPO="$(prop GHOSTTY_REPO)"
ZIG_VERSION="$(prop ZIG_VERSION)"
NDK_VERSION="$(prop NDK_VERSION)"
ABIS_CSV="$(prop ABIS)"

if [ -z "$GHOSTTY_COMMIT" ] || [ -z "$GHOSTTY_REPO" ]; then
  echo "build-libghostty: GHOSTTY_COMMIT / GHOSTTY_REPO missing from toolchain.properties" >&2
  exit 1
fi

# 40-char sha only — never a branch or tag, the C ABI is unstable and must be
# pinned exactly.
if ! [[ "$GHOSTTY_COMMIT" =~ ^[0-9a-f]{40}$ ]]; then
  echo "build-libghostty: GHOSTTY_COMMIT '$GHOSTTY_COMMIT' is not a 40-char sha" >&2
  exit 1
fi

BUILD_DIR="$SCRIPT_DIR/.build"
SRC_DIR="$BUILD_DIR/ghostty-src"
VENDOR_DIR="$SCRIPT_DIR/vendor"

IFS=',' read -r -a ABIS <<<"$ABIS_CSV"

declare -A ABI_TARGET=(
  [arm64-v8a]=aarch64-linux-android
  [x86_64]=x86_64-linux-android
)

echo "build-libghostty: fetching pinned commit $GHOSTTY_COMMIT (shallow, no branch)" >&2
mkdir -p "$BUILD_DIR"
if [ ! -d "$SRC_DIR/.git" ]; then
  rm -rf "$SRC_DIR"
  mkdir -p "$SRC_DIR"
  git -C "$SRC_DIR" init -q
  git -C "$SRC_DIR" remote add origin "$GHOSTTY_REPO"
fi

CURRENT_HEAD="$(git -C "$SRC_DIR" rev-parse HEAD 2>/dev/null || true)"
if [ "$CURRENT_HEAD" != "$GHOSTTY_COMMIT" ]; then
  git -C "$SRC_DIR" fetch --depth 1 origin "$GHOSTTY_COMMIT"
  git -C "$SRC_DIR" checkout -q --detach FETCH_HEAD
fi

RESOLVED_HEAD="$(git -C "$SRC_DIR" rev-parse HEAD)"
if [ "$RESOLVED_HEAD" != "$GHOSTTY_COMMIT" ]; then
  echo "build-libghostty: checked-out HEAD $RESOLVED_HEAD does not match pinned $GHOSTTY_COMMIT" >&2
  exit 1
fi

rm -rf "$VENDOR_DIR/arm64-v8a" "$VENDOR_DIR/x86_64" "$VENDOR_DIR/include"
mkdir -p "$VENDOR_DIR/arm64-v8a" "$VENDOR_DIR/x86_64"

for abi in "${ABIS[@]}"; do
  target="${ABI_TARGET[$abi]:-}"
  if [ -z "$target" ]; then
    echo "build-libghostty: no zig target mapping for ABI '$abi'" >&2
    exit 1
  fi

  out_dir="$BUILD_DIR/out-$abi"
  rm -rf "$out_dir"

  echo "build-libghostty: building $abi (zig target $target, bare triple — no API-level suffix; see docs/android-toolchain.md)" >&2
  (
    cd "$SRC_DIR"
    zig build -Demit-lib-vt -Dtarget="$target" -Doptimize=ReleaseFast --prefix "$out_dir"
  )

  ARTIFACT="$out_dir/lib/libghostty-vt.a"
  if [ ! -s "$ARTIFACT" ]; then
    echo "build-libghostty: ABI '$abi' produced no libghostty-vt.a at $ARTIFACT — refusing to vendor a partial tree" >&2
    exit 1
  fi

  cp "$ARTIFACT" "$VENDOR_DIR/$abi/libghostty-vt.a"

  if [ ! -d "$VENDOR_DIR/include" ]; then
    cp -R "$out_dir/include" "$VENDOR_DIR/include"
  fi
done

for abi in "${ABIS[@]}"; do
  if [ ! -s "$VENDOR_DIR/$abi/libghostty-vt.a" ]; then
    echo "build-libghostty: post-build check failed — $VENDOR_DIR/$abi/libghostty-vt.a missing or empty" >&2
    exit 1
  fi
done
if [ ! -d "$VENDOR_DIR/include/ghostty" ]; then
  echo "build-libghostty: post-build check failed — $VENDOR_DIR/include/ghostty missing" >&2
  exit 1
fi

echo "build-libghostty: writing vendor-manifest.json" >&2
BUILD_TIMESTAMP="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

python3 - "$VENDOR_DIR" "$GHOSTTY_COMMIT" "$GHOSTTY_REPO" "$ZIG_VERSION" "$NDK_VERSION" "$BUILD_TIMESTAMP" "${ABIS[@]}" <<'PYEOF'
import hashlib
import json
import sys
from pathlib import Path

vendor_dir = Path(sys.argv[1])
ghostty_commit, ghostty_repo, zig_version, ndk_version, build_timestamp = sys.argv[2:7]
abis = sys.argv[7:]

def sha256_file(p: Path) -> str:
    h = hashlib.sha256()
    h.update(p.read_bytes())
    return h.hexdigest()

def sha256_tree(root: Path) -> str:
    h = hashlib.sha256()
    for f in sorted(root.rglob("*")):
        if f.is_file():
            h.update(str(f.relative_to(root)).encode("utf-8"))
            h.update(f.read_bytes())
    return h.hexdigest()

abi_entries = {}
for abi in abis:
    artifact = vendor_dir / abi / "libghostty-vt.a"
    abi_entries[abi] = {
        "path": f"{abi}/libghostty-vt.a",
        "sha256": sha256_file(artifact),
        "size_bytes": artifact.stat().st_size,
    }

manifest = {
    "ghostty_commit": ghostty_commit,
    "ghostty_repo": ghostty_repo,
    "zig_version": zig_version,
    "ndk_version": ndk_version,
    "build_timestamp": build_timestamp,
    "header_sha256": sha256_tree(vendor_dir / "include"),
    "abis": abi_entries,
}

(vendor_dir / "vendor-manifest.json").write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
PYEOF

echo "build-libghostty: done. Artifacts in $VENDOR_DIR" >&2
