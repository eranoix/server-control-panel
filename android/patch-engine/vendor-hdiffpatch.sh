#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

PROPS_FILE="$SCRIPT_DIR/toolchain.properties"
prop() { grep -E "^$1=" "$PROPS_FILE" | tail -1 | cut -d= -f2-; }

BUILD_DIR="$SCRIPT_DIR/.build"
SRC_DIR="$BUILD_DIR/src"
VENDOR_DIR="$SCRIPT_DIR/vendor"

if [ "${1:-}" = "--relist" ]; then
  cat <<'RELIST'
To regenerate vendor-files.txt after changing a switch in Android.mk:

  1. ./vendor-hdiffpatch.sh                 # ensures .build/src at the pinned commits
  2. cd .build/src/HDiffPatch/builds/android_ndk_jni_mk
     ndk-build NDK_PROJECT_PATH=. APP_BUILD_SCRIPT=Android.mk \
       NDK_APPLICATION_MK=Application.mk APP_ABI="arm64-v8a x86_64" \
       <the same switches as src/main/cpp/Android.mk>
  3. Merge all obj/local/**/*.o.d files, normalize the relative paths and
     write the sorted union to vendor-files.txt (prefix = sibling repo
     name: HDiffPatch/, lzma/, zstd/, xxHash/, libmd5/).

The .d files are the only reliable source for this list: hpatchz.c includes
depend on the -D flags the switches set, so reading the code by eye misses some.
RELIST
  exit 0
fi

mkdir -p "$SRC_DIR"

fetch_pinned() {
  local name="$1" repo="$2" commit="$3"
  local dst="$SRC_DIR/$name"
  if [ -d "$dst/.$VCS_DIR_SUFFIX" ]; then
    local head
    head="$(cd "$dst" && "$VCS" rev-parse HEAD)"
    if [ "$head" = "$commit" ]; then
      echo "vendor-hdiffpatch: $name already at $commit" >&2
      return 0
    fi
  fi
  rm -rf "$dst"
  mkdir -p "$dst"
  ( cd "$dst"
    "$VCS" init -q
    "$VCS" remote add origin "$repo"
    "$VCS" fetch -q --depth 1 origin "$commit"
    "$VCS" checkout -q FETCH_HEAD )
  echo "vendor-hdiffpatch: $name -> $commit" >&2
}

VCS=git
VCS_DIR_SUFFIX=git

fetch_pinned HDiffPatch "$(prop HDIFFPATCH_REPO)" "$(prop HDIFFPATCH_COMMIT)"
fetch_pinned lzma       "$(prop LZMA_REPO)"       "$(prop LZMA_COMMIT)"
fetch_pinned zstd       "$(prop ZSTD_REPO)"       "$(prop ZSTD_COMMIT)"
fetch_pinned xxHash     "$(prop XXHASH_REPO)"     "$(prop XXHASH_COMMIT)"
fetch_pinned libmd5     "$(prop LIBMD5_REPO)"     "$(prop LIBMD5_COMMIT)"

echo "vendor-hdiffpatch: copying $(grep -cvE '^\s*(#|$)' vendor-files.txt) files" >&2
rm -rf "$VENDOR_DIR/HDiffPatch" "$VENDOR_DIR/lzma" "$VENDOR_DIR/zstd" \
       "$VENDOR_DIR/xxHash" "$VENDOR_DIR/libmd5"
mkdir -p "$VENDOR_DIR"

while IFS= read -r rel; do
  case "$rel" in ''|\#*) continue ;; esac
  mkdir -p "$VENDOR_DIR/$(dirname "$rel")"
  cp -p "$SRC_DIR/$rel" "$VENDOR_DIR/$rel"
done < vendor-files.txt

JAVA_REL="HDiffPatch/builds/android_ndk_jni_mk/java"
mkdir -p "$VENDOR_DIR/$JAVA_REL"
cp -pR "$SRC_DIR/$JAVA_REL/." "$VENDOR_DIR/$JAVA_REL/"

cp -p "$SRC_DIR/HDiffPatch/LICENSE" "$VENDOR_DIR/HDiffPatch/LICENSE"
cp -p "$SRC_DIR/zstd/LICENSE"       "$VENDOR_DIR/zstd/LICENSE"
cp -p "$SRC_DIR/xxHash/LICENSE"     "$VENDOR_DIR/xxHash/LICENSE"
cp -p "$SRC_DIR/lzma/DOC/lzma-sdk.txt" "$VENDOR_DIR/lzma/LICENSE-lzma-sdk.txt"

python3 - "$VENDOR_DIR" "$PROPS_FILE" <<'PY'
import hashlib, json, os, subprocess, sys

vendor, props_file = sys.argv[1], sys.argv[2]
props = {}
for line in open(props_file):
    line = line.strip()
    if line and not line.startswith("#") and "=" in line:
        k, v = line.split("=", 1)
        props[k] = v

files = {}
for root, _dirs, names in os.walk(vendor):
    for n in sorted(names):
        p = os.path.join(root, n)
        rel = os.path.relpath(p, vendor)
        if rel == "vendor-manifest.json":
            continue
        with open(p, "rb") as fh:
            data = fh.read()
        files[rel] = {"sha256": hashlib.sha256(data).hexdigest(), "size_bytes": len(data)}

manifest = {
    "commits": {
        "HDiffPatch": props["HDIFFPATCH_COMMIT"],
        "lzma": props["LZMA_COMMIT"],
        "zstd": props["ZSTD_COMMIT"],
        "xxHash": props["XXHASH_COMMIT"],
        "libmd5": props["LIBMD5_COMMIT"],
    },
    "repos": {
        "HDiffPatch": props["HDIFFPATCH_REPO"],
        "lzma": props["LZMA_REPO"],
        "zstd": props["ZSTD_REPO"],
        "xxHash": props["XXHASH_REPO"],
        "libmd5": props["LIBMD5_REPO"],
    },
    "hdiffpatch_tag": props.get("HDIFFPATCH_TAG", ""),
    "ndk_version": props["NDK_VERSION"],
    "file_count": len(files),
    "total_bytes": sum(f["size_bytes"] for f in files.values()),
    "files": dict(sorted(files.items())),
}
out = os.path.join(vendor, "vendor-manifest.json")
with open(out, "w") as fh:
    json.dump(manifest, fh, indent=2, sort_keys=False)
    fh.write("\n")
print("vendor-hdiffpatch: %d files, %d bytes" % (len(files), manifest["total_bytes"]), file=sys.stderr)
PY

echo "vendor-hdiffpatch: done" >&2
