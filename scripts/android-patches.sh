#!/usr/bin/env bash
# android-patches.sh: builds the Android app's incremental update catalogue from
# what was JUST published in data/fdroid/repo/.
#
# Final step of scripts/android-publish.sh, run AFTER the apksigner fingerprint
# gate. Patches must be built from the bytes of the ALREADY SIGNED APK; signing
# is offline by design (docs/android-signing-keystore.md §1), so this host only
# has the final bytes after publication.
#
# OUTPUT (data/android-updates/)
#   apks/<sha256>.apk                    archived signed APK, by hash
#   patches/<baseSha>-<targetSha>.hdiff  direct patch base -> new version
#   full/<targetSha>.hdiff               full rebuild (empty base)
#   manifest.json                        index read by internal/androidupdate
#
# Keyed by SHA-256, not versionCode: a patch depends on the exact base bytes,
# and two builds with the same versionCode differ. Applying the wrong patch
# yields a corrupt file, not a version error. The app sends the hash of its
# installed APK; with no patch for that exact hash it falls back to the full one.
#
# Direct patches, not chained: every hop is one more failure point and one more
# patch application on the device.
#
# The "full" artifact is also .hdiff (hdiffz with an empty base, about a third of
# the raw APK size) so the device has ONE code path (hpatchz) for both cases.
#
# APKs are archived under apks/ because the NEXT version's patches need the
# previous bytes, and data/fdroid/repo/ is replaced wholesale (rsync
# --delete-after) by the operator's upload. Hardlinks make archiving free.
#
# Environment overrides (for tests; production uses the defaults):
#   FDROID_REPO_DIR, ANDROID_UPDATES_DIR, PACKAGE_ID, PATCH_WINDOW, HDIFFZ
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FDROID_REPO_DIR="${FDROID_REPO_DIR:-$ROOT_DIR/data/fdroid/repo}"
UPDATES_DIR="${ANDROID_UPDATES_DIR:-$ROOT_DIR/data/android-updates}"
HDIFFZ="${HDIFFZ:-hdiffz}"

# The default PACKAGE_ID comes from android/gradle.properties, the single source
# of truth also used by internal/api/handlers_android_install.go (pinned by
# TestAndroidPackageID). Never repeat the literal here.
default_package_id() {
  sed -n 's/^servercontrolpanel\.applicationId=//p' "$ROOT_DIR/android/gradle.properties" | head -1 | tr -d '[:space:]'
}
PACKAGE_ID="${PACKAGE_ID:-$(default_package_id)}"

# PATCH_WINDOW is how many of the NEWEST versions stay in the catalogue,
# including the target. With 5: the target plus 4 bases; everything whose base
# left the window is deleted.
PATCH_WINDOW="${PATCH_WINDOW:-5}"

# Compression options, measured on this project (0.1.5 -> 0.1.6):
#   -c-zstd-21-24     1 583 566 B      -c-lzma2-9-64m     1 415 213 B
#   -SD -c-zstd-21-24 1 551 505 B      -SD -c-lzma2-9-64m 1 400 329 B  <-- chosen
# Full artifact (empty base): -SD -c-lzma2-9-64m = 10 029 237 B vs 31 135 416 B raw.
#
# -SD (single compressed diff) is also what hpatchz applies with a single
# decompression buffer and step by step during download. The prebuilt
# libhpatchz.so of the official Android SDK (v5.1.3, arm64-v8a) supports lzma2
# and -SD; if that changes, this line and the manifest's patch_tool field are
# the single point of adjustment.
HDIFF_OPTS=(-SD -c-lzma2-9-64m)

fail() { echo "ERROR: $*" >&2; exit 1; }

command -v "$HDIFFZ" >/dev/null 2>&1 || fail "hdiffz not found (\$HDIFFZ=$HDIFFZ); run scripts/setup-hdiffpatch.sh"
command -v python3 >/dev/null 2>&1 || fail "python3 not found"
command -v sha256sum >/dev/null 2>&1 || fail "sha256sum not found"

INDEX_JSON="$FDROID_REPO_DIR/index-v2.json"
[ -f "$INDEX_JSON" ] || fail "$INDEX_JSON does not exist: nothing published yet; run scripts/android-publish.sh first"

[ -n "$PACKAGE_ID" ] || fail "PACKAGE_ID is empty and android/gradle.properties does not define servercontrolpanel.applicationId"

mkdir -p "$UPDATES_DIR/apks" "$UPDATES_DIR/patches" "$UPDATES_DIR/full"

# hdiffz with no arguments prints its usage banner and exits non-zero, which
# would silently abort under `set -e -o pipefail`; hence the `|| true`.
HDIFF_VERSION="$({ "$HDIFFZ" 2>&1 || true; } | head -1 | tr -d '\r')"
PATCH_TOOL="$HDIFF_VERSION ${HDIFF_OPTS[*]}"

# 1. Version window, newest first.
# One line per version: "versionCode<TAB>versionName<TAB>apkFile". Sorted in
# Python (numerically by versionCode) so it does not depend on the locale.
VERSIONS="$(python3 - "$INDEX_JSON" "$PACKAGE_ID" "$PATCH_WINDOW" <<'PY'
import json, sys
idx_path, pkg_id, window = sys.argv[1], sys.argv[2], int(sys.argv[3])
with open(idx_path, encoding="utf-8") as fh:
    idx = json.load(fh)
pkg = idx.get("packages", {}).get(pkg_id)
if not pkg:
    sys.stderr.write(
        "ERROR: index-v2.json does not contain package %r.\n"
        "       Packages present: %s\n" % (pkg_id, ", ".join(sorted(idx.get("packages", {}))) or "(none)")
    )
    sys.exit(3)
seen = {}
for v in pkg.get("versions", {}).values():
    man = v.get("manifest", {})
    code = man.get("versionCode")
    apk_file = (v.get("file", {}) or {}).get("name", "").lstrip("/")
    if code is None or not apk_file:
        continue
    # A repeated versionCode keeps the first entry: deterministic beats guessing.
    seen.setdefault(int(code), (man.get("versionName", ""), apk_file))
for code in sorted(seen, reverse=True)[:window]:
    name, apk_file = seen[code]
    print("%d\t%s\t%s" % (code, name, apk_file))
PY
)" || fail "could not read the versions from $INDEX_JSON"

[ -n "$VERSIONS" ] || fail "no version of $PACKAGE_ID in $INDEX_JSON"

# 2. Archive every APK in the window as apks/<sha256>.apk.
# Work TSV: sha256, versionCode, versionName, size.
WORK="$(mktemp "${TMPDIR:-/tmp}/panel-android-patches.XXXXXX")"
trap 'rm -f "$WORK" "$WORK.manifest"' EXIT

while IFS=$'\t' read -r code name apk_file; do
  [ -n "$code" ] || continue
  src="$FDROID_REPO_DIR/$apk_file"
  if [ ! -f "$src" ]; then
    # The version is in the index but the operator's upload lacks the APK. Use
    # the archived copy if there is one, found through the .versioncode file
    # next to it (the hash cannot be recomputed without the original);
    # otherwise that base gets no patch and the app falls back to the full one.
    found=""
    for cand in "$UPDATES_DIR/apks"/*.apk; do
      [ -f "$cand" ] || continue
      cand_code="$(cat "$cand.versioncode" 2>/dev/null || true)"
      if [ "$cand_code" = "$code" ]; then found="$cand"; break; fi
    done
    if [ -z "$found" ]; then
      echo "WARNING: versionCode $code is in the index but $src does not exist and there is no archived copy; no patch from that base" >&2
      continue
    fi
    src="$found"
  fi
  sha="$(sha256sum "$src" | cut -d' ' -f1)"
  size="$(stat -c%s "$src")"
  dest="$UPDATES_DIR/apks/$sha.apk"
  if [ ! -f "$dest" ]; then
    # Hardlink first (same filesystem, no extra disk): a later rsync
    # --delete-after removes the fdroid/repo name, the inode survives here.
    ln "$src" "$dest" 2>/dev/null || cp -f "$src" "$dest"
  fi
  printf '%s\n' "$code" > "$dest.versioncode"
  printf '%s\t%s\t%s\t%s\n' "$sha" "$code" "$name" "$size" >> "$WORK"
done <<< "$VERSIONS"

[ -s "$WORK" ] || fail "no APK in the window could be located; nothing to generate"

# 3. Target = first line (highest versionCode).
IFS=$'\t' read -r TARGET_SHA TARGET_CODE TARGET_NAME TARGET_SIZE < "$WORK"
TARGET_APK="$UPDATES_DIR/apks/$TARGET_SHA.apk"

echo "==> target: $TARGET_NAME (versionCode $TARGET_CODE) sha256=$TARGET_SHA"

# 4. Full rebuild (empty base).
FULL_REL="full/$TARGET_SHA.hdiff"
FULL_ABS="$UPDATES_DIR/$FULL_REL"
if [ -s "$FULL_ABS" ]; then
  echo "    full artifact already exists: $FULL_REL"
else
  echo "    generating full artifact (empty base)..."
  # Write to .tmp and rename, so an interrupted run never leaves a truncated
  # .hdiff under its final name for the next manifest to publish.
  "$HDIFFZ" "${HDIFF_OPTS[@]}" "" "$TARGET_APK" "$FULL_ABS.tmp" >/dev/null \
    || fail "hdiffz failed generating the full artifact"
  mv -f "$FULL_ABS.tmp" "$FULL_ABS"
fi

# 5. One direct patch from every base in the window.
: > "$WORK.manifest"
printf 'full\t%s\t\t\t\n' "$FULL_REL" >> "$WORK.manifest"

while IFS=$'\t' read -r sha code name size; do
  [ "$sha" = "$TARGET_SHA" ] && continue
  base_apk="$UPDATES_DIR/apks/$sha.apk"
  [ -f "$base_apk" ] || continue
  rel="patches/$sha-$TARGET_SHA.hdiff"
  abs="$UPDATES_DIR/$rel"
  if [ -s "$abs" ]; then
    echo "    patch already exists: $name ($code)"
  else
    echo "    generating patch from $name ($code)..."
    "$HDIFFZ" "${HDIFF_OPTS[@]}" "$base_apk" "$TARGET_APK" "$abs.tmp" >/dev/null \
      || fail "hdiffz failed generating the patch from $sha"
    mv -f "$abs.tmp" "$abs"
  fi
  printf 'patch\t%s\t%s\t%s\t%s\n' "$rel" "$sha" "$name" "$code" >> "$WORK.manifest"
done < "$WORK"

# 6. manifest.json, written atomically.
python3 - "$UPDATES_DIR" "$PACKAGE_ID" "$PATCH_TOOL" "$TARGET_SHA" "$TARGET_CODE" "$TARGET_NAME" "$TARGET_SIZE" "$WORK.manifest" <<'PY'
import hashlib, json, os, sys, tempfile, time

(updates_dir, package_id, patch_tool, target_sha, target_code,
 target_name, target_size, lines_path) = sys.argv[1:9]


def describe(rel):
    path = os.path.join(updates_dir, rel)
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for block in iter(lambda: fh.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest(), os.path.getsize(path)


full = None
patches = []
with open(lines_path, encoding="utf-8") as fh:
    for line in fh:
        if not line.strip():
            continue
        kind, rel, from_sha, from_name, from_code = (line.rstrip("\n").split("\t") + [""] * 5)[:5]
        sha, size = describe(rel)
        art = {"kind": kind, "file": rel, "size_bytes": size, "sha256": sha}
        if kind == "full":
            full = art
        else:
            art["from_sha256"] = from_sha
            art["from_version_name"] = from_name
            art["from_version_code"] = int(from_code)
            patches.append(art)

if full is None:
    sys.exit("ERROR: full artifact missing while building the manifest")

# Stable order (newest base first) so the manifest bytes only change when the
# content does.
patches.sort(key=lambda a: a["from_version_code"], reverse=True)

manifest = {
    "schema_version": 1,
    "generated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    "package_id": package_id,
    "patch_tool": patch_tool,
    "latest": {
        "version_name": target_name,
        "version_code": int(target_code),
        "sha256": target_sha,
        "size_bytes": int(target_size),
    },
    "full": full,
    "patches": patches,
}

dest = os.path.join(updates_dir, "manifest.json")
fd, tmp = tempfile.mkstemp(dir=updates_dir, prefix=".manifest-", suffix=".json")
try:
    with os.fdopen(fd, "w", encoding="utf-8") as fh:
        json.dump(manifest, fh, indent=2, ensure_ascii=False, sort_keys=True)
        fh.write("\n")
        fh.flush()
        os.fsync(fh.fileno())
    os.chmod(tmp, 0o644)
    # Atomic rename: a concurrent reader (the server) sees the whole old
    # manifest or the whole new one, never a mix.
    os.replace(tmp, dest)
except BaseException:
    if os.path.exists(tmp):
        os.unlink(tmp)
    raise
print("    manifest.json: %d patch(es) + full" % len(patches))
PY

# 7. Retention: delete everything the new manifest does not reference,
# including patches whose BASE left the window and older targets' full artifacts.
python3 - "$UPDATES_DIR" <<'PY'
import json, os, sys

updates_dir = sys.argv[1]
with open(os.path.join(updates_dir, "manifest.json"), encoding="utf-8") as fh:
    m = json.load(fh)

keep = {m["full"]["file"]}
keep.update(p["file"] for p in m["patches"])
live_apks = {m["latest"]["sha256"] + ".apk"}
live_apks.update(p["from_sha256"] + ".apk" for p in m["patches"])

removed = 0
for sub in ("patches", "full"):
    d = os.path.join(updates_dir, sub)
    for name in os.listdir(d):
        rel = sub + "/" + name
        if rel in keep:
            continue
        os.unlink(os.path.join(d, name))
        removed += 1

d = os.path.join(updates_dir, "apks")
for name in os.listdir(d):
    base = name[:-len(".versioncode")] if name.endswith(".versioncode") else name
    if base in live_apks:
        continue
    os.unlink(os.path.join(d, name))
    removed += 1

print("    retention: %d file(s) outside the window removed" % removed)
PY

echo "OK: incremental update catalogue in $UPDATES_DIR"
