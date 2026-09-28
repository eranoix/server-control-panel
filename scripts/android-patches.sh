#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FDROID_REPO_DIR="${FDROID_REPO_DIR:-$ROOT_DIR/data/fdroid/repo}"
UPDATES_DIR="${ANDROID_UPDATES_DIR:-$ROOT_DIR/data/android-updates}"
HDIFFZ="${HDIFFZ:-hdiffz}"

default_package_id() {
  sed -n 's/^servercontrolpanel\.applicationId=//p' "$ROOT_DIR/android/gradle.properties" | head -1 | tr -d '[:space:]'
}
PACKAGE_ID="${PACKAGE_ID:-$(default_package_id)}"

PATCH_WINDOW="${PATCH_WINDOW:-5}"

HDIFF_OPTS=(-SD -c-lzma2-9-64m)

fail() { echo "ERROR: $*" >&2; exit 1; }

command -v "$HDIFFZ" >/dev/null 2>&1 || fail "hdiffz not found (\$HDIFFZ=$HDIFFZ); run scripts/setup-hdiffpatch.sh"
command -v python3 >/dev/null 2>&1 || fail "python3 not found"
command -v sha256sum >/dev/null 2>&1 || fail "sha256sum not found"

INDEX_JSON="$FDROID_REPO_DIR/index-v2.json"
[ -f "$INDEX_JSON" ] || fail "$INDEX_JSON does not exist: nothing published yet; run scripts/android-publish.sh first"

[ -n "$PACKAGE_ID" ] || fail "PACKAGE_ID is empty and android/gradle.properties does not define servercontrolpanel.applicationId"

mkdir -p "$UPDATES_DIR/apks" "$UPDATES_DIR/patches" "$UPDATES_DIR/full"

HDIFF_VERSION="$({ "$HDIFFZ" 2>&1 || true; } | head -1 | tr -d '\r')"
PATCH_TOOL="$HDIFF_VERSION ${HDIFF_OPTS[*]}"

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

WORK="$(mktemp "${TMPDIR:-/tmp}/panel-android-patches.XXXXXX")"
trap 'rm -f "$WORK" "$WORK.manifest"' EXIT

while IFS=$'\t' read -r code name apk_file; do
  [ -n "$code" ] || continue
  src="$FDROID_REPO_DIR/$apk_file"
  if [ ! -f "$src" ]; then
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
    ln "$src" "$dest" 2>/dev/null || cp -f "$src" "$dest"
  fi
  printf '%s\n' "$code" > "$dest.versioncode"
  printf '%s\t%s\t%s\t%s\n' "$sha" "$code" "$name" "$size" >> "$WORK"
done <<< "$VERSIONS"

[ -s "$WORK" ] || fail "no APK in the window could be located; nothing to generate"

IFS=$'\t' read -r TARGET_SHA TARGET_CODE TARGET_NAME TARGET_SIZE < "$WORK"
TARGET_APK="$UPDATES_DIR/apks/$TARGET_SHA.apk"

echo "==> target: $TARGET_NAME (versionCode $TARGET_CODE) sha256=$TARGET_SHA"

FULL_REL="full/$TARGET_SHA.hdiff"
FULL_ABS="$UPDATES_DIR/$FULL_REL"
if [ -s "$FULL_ABS" ]; then
  echo "    full artifact already exists: $FULL_REL"
else
  echo "    generating full artifact (empty base)..."
  "$HDIFFZ" "${HDIFF_OPTS[@]}" "" "$TARGET_APK" "$FULL_ABS.tmp" >/dev/null \
    || fail "hdiffz failed generating the full artifact"
  mv -f "$FULL_ABS.tmp" "$FULL_ABS"
fi

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
