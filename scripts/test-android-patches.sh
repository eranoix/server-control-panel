#!/usr/bin/env bash
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT/scripts/android-patches.sh"
PACKAGE="tech.northwind.servercontrolpanel"

[ -f "$SCRIPT" ] || { echo "cannot find $SCRIPT"; exit 2; }
command -v hdiffz  >/dev/null 2>&1 || { echo "hdiffz not found; run scripts/setup-hdiffpatch.sh"; exit 2; }
command -v hpatchz >/dev/null 2>&1 || { echo "hpatchz not found; run scripts/setup-hdiffpatch.sh"; exit 2; }
command -v python3 >/dev/null 2>&1 || { echo "python3 not found"; exit 2; }

pass=0; fail=0
ok() { echo "  OK: $1"; pass=$((pass+1)); }
no() { echo "  FAILED: $1"; fail=$((fail+1)); }
echo "=== test-android-patches ==="

TMP="$(mktemp -d "${TMPDIR:-/tmp}/panel-android-patches-test.XXXXXX")" || exit 2
trap 'case "$TMP" in "${TMPDIR:-/tmp}"/panel-android-patches-test.*) rm -rf "$TMP";; esac' EXIT

REPO="$TMP/repo"
UPDATES="$TMP/updates"
mkdir -p "$REPO" "$UPDATES"

make_apk() {
  local code="$1"
  local dest="$REPO/servercontrolpanel-$code.apk"
  python3 - "$dest" "$code" <<'PY'
import sys
dest, code = sys.argv[1], int(sys.argv[2])
shared = (b"server-control-panel-shared-payload-" * 4096)[:2 * 1024 * 1024]
own = (("version-%d-" % code).encode() * 4096)[:128 * 1024]
with open(dest, "wb") as fh:
    fh.write(shared[: 1024 * 1024])
    fh.write(own)
    fh.write(shared[1024 * 1024 :])
PY
}

write_index() {
  python3 - "$REPO/index-v2.json" "$PACKAGE" "$@" <<'PY'
import json, sys
dest, pkg = sys.argv[1], sys.argv[2]
versions = {}
for code in sys.argv[3:]:
    versions["v" + code] = {
        "manifest": {"versionName": "0.1." + code, "versionCode": int(code)},
        "file": {"name": "/servercontrolpanel-%s.apk" % code},
    }
with open(dest, "w", encoding="utf-8") as fh:
    json.dump({"packages": {pkg: {"versions": versions}}}, fh)
PY
}

run() { FDROID_REPO_DIR="$REPO" ANDROID_UPDATES_DIR="$UPDATES" "$SCRIPT" "$@"; }

field() { python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));exec("v=d"+sys.argv[2]);print(v)' "$UPDATES/manifest.json" "$1"; }

make_apk 1; make_apk 2
write_index 1 2
if ! run > "$TMP/run1.log" 2>&1; then
  echo "  FAILED: first run of the script"; cat "$TMP/run1.log"; exit 1
fi

TARGET_SHA="$(field '["latest"]["sha256"]')"
FULL_FILE="$(field '["full"]["file"]')"
PATCH_FILE="$(field '["patches"][0]["file"]')"
BASE_SHA="$(field '["patches"][0]["from_sha256"]')"

if hpatchz "$UPDATES/apks/$BASE_SHA.apk" "$UPDATES/$PATCH_FILE" "$TMP/recon-patch.bin" >/dev/null 2>&1 \
   && [ "$(sha256sum "$TMP/recon-patch.bin" | cut -d' ' -f1)" = "$TARGET_SHA" ]; then
  ok "patch applied with hpatchz rebuilds a SHA-256 identical to the target"
else
  no "patch does NOT rebuild the target"
fi

if hpatchz "" "$UPDATES/$FULL_FILE" "$TMP/recon-full.bin" >/dev/null 2>&1 \
   && [ "$(sha256sum "$TMP/recon-full.bin" | cut -d' ' -f1)" = "$TARGET_SHA" ]; then
  ok "full artifact (empty base) rebuilds a SHA-256 identical to the target"
else
  no "full artifact does NOT rebuild the target"
fi

if python3 - "$UPDATES" <<'PY'
import hashlib, json, os, sys
d = sys.argv[1]
m = json.load(open(os.path.join(d, "manifest.json"), encoding="utf-8"))
for art in [m["full"]] + m["patches"]:
    path = os.path.join(d, art["file"])
    if not os.path.isfile(path):
        sys.exit("artifact %s does not exist" % art["file"])
    if os.path.getsize(path) != art["size_bytes"]:
        sys.exit("size_bytes of %s does not match" % art["file"])
    h = hashlib.sha256(open(path, "rb").read()).hexdigest()
    if h != art["sha256"]:
        sys.exit("sha256 of %s does not match" % art["file"])
if m["schema_version"] != 1 or not m["patch_tool"]:
    sys.exit("incomplete manifest header")
PY
then ok "consistent manifest: sha256/size_bytes match the files"
else no "manifest inconsistent with the files on disk"
fi

before="$(sha256sum "$UPDATES/$PATCH_FILE" | cut -d' ' -f1)"
if run > "$TMP/run2.log" 2>&1 \
   && [ "$(sha256sum "$UPDATES/$PATCH_FILE" | cut -d' ' -f1)" = "$before" ] \
   && grep -q "already exists" "$TMP/run2.log"; then
  ok "running again is idempotent (reuses the existing artifacts)"
else
  no "second run was not idempotent"; cat "$TMP/run2.log"
fi

for c in 3 4 5; do make_apk "$c"; done
write_index 1 2 3 4 5
run > "$TMP/run5.log" 2>&1 || { echo "  FAILED: run with 5 versions"; cat "$TMP/run5.log"; }

n_patches="$(python3 -c 'import json,sys;print(len(json.load(open(sys.argv[1]))["patches"]))' "$UPDATES/manifest.json")"
v1_patch="$(python3 -c '
import json,sys
d=json.load(open(sys.argv[1]))
print(next((p["file"] for p in d["patches"] if p["from_version_code"]==1), ""))' "$UPDATES/manifest.json")"
if [ "$n_patches" = "4" ] && [ -n "$v1_patch" ] && [ -f "$UPDATES/$v1_patch" ]; then
  ok "window of 5: 4 patches generated, the oldest base still inside"
else
  no "unexpected window of 5 (patches=$n_patches, v1 patch=$v1_patch)"
fi

v1_sha="$(sha256sum "$REPO/servercontrolpanel-1.apk" | cut -d' ' -f1)"
old_full="$FULL_FILE"
make_apk 6
write_index 1 2 3 4 5 6
run > "$TMP/run6.log" 2>&1 || { echo "  FAILED: run with 6 versions"; cat "$TMP/run6.log"; }

errors=""
[ -f "$UPDATES/$v1_patch" ] && errors="$errors v1-patch-survived"
[ -f "$UPDATES/apks/$v1_sha.apk" ] && errors="$errors v1-apk-survived"
[ -f "$UPDATES/$old_full" ] && errors="$errors old-target-full-survived"
python3 -c '
import json,sys
d=json.load(open(sys.argv[1]))
assert d["latest"]["version_code"]==6, d["latest"]
assert len(d["patches"])==4, len(d["patches"])
assert sorted(p["from_version_code"] for p in d["patches"])==[2,3,4,5], d["patches"]
' "$UPDATES/manifest.json" 2>/dev/null || errors="$errors wrong-6th-manifest"

if [ -z "$errors" ]; then
  ok "6th version: patches/APK of the base outside the window deleted, manifest only has 2..5"
else
  no "retention failed:$errors"
fi

if [ -z "$(find "$UPDATES" -name '*.tmp' -o -name '.manifest-*' 2>/dev/null)" ]; then
  ok "no temporary file left in the updates directory"
else
  no "temporary files left: $(find "$UPDATES" -name '*.tmp' -o -name '.manifest-*')"
fi

rm -f "$REPO/servercontrolpanel-2.apk" "$UPDATES/apks"/*.apk.versioncode
if run > "$TMP/run7.log" 2>&1; then
  ok "missing APK in the repository does not break generation (only that base degrades)"
else
  no "generation died with an APK missing from the repository"; cat "$TMP/run7.log"
fi

echo
echo "=== $pass OK, $fail failure(s) ==="
[ "$fail" -eq 0 ] || exit 1
