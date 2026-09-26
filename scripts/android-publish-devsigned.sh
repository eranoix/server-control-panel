#!/usr/bin/env bash
# android-publish-devsigned.sh <versionName> <versionCode> [apk]: publishes a
# release signed with the DEVELOPMENT KEY to BOTH places that need to know it.
#
# `scripts/android-publish.sh` is the operator path: it needs the offline-signed
# APK and an F-Droid index regenerated with the repokey, neither of which exists
# on this server by design (docs/android-signing-keystore.md). Until the release
# key is used, working builds are signed with the throwaway key from
# docs/android-dev-key.md and published from here.
#
# Publishing only to the F-Droid repository is not enough: the app checks the
# INCREMENTAL CHANNEL (`data/android-updates/manifest.json`, served by
# `/api/mobile/v1/app/update`), a separate artifact built by
# `scripts/android-patches.sh`. This script does both.
#
# The index keeps a window of versions, not only the new one: android-patches.sh
# builds its patch window from the versions in `index-v2.json`. With one version
# the channel only has the full artifact (~10 MB); with the window an update is
# an incremental patch (~1.5 MB measured).
#
# Old .apk files still leave the public directory: android-patches.sh reads the
# old bytes from `data/android-updates/apks/` (its own archive, by hash). The
# index keeps the MEMORY of the versions; the public disk keeps only the current one.
#
# Environment overrides (for tests): FDROID_REPO_DIR, UPDATES_DIR, PACKAGE_ID,
# JANELA (window size), APKSIGNER.
set -euo pipefail

fail_early() { echo "ERROR: $*" >&2; exit 1; }

VERSION_NAME="${1:?usage: scripts/android-publish-devsigned.sh <versionName> <versionCode> [apk]}"
VERSION_CODE="${2:?usage: scripts/android-publish-devsigned.sh <versionName> <versionCode> [apk]}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APK="${3:-$ROOT_DIR/android/app/build/outputs/apk/release/app-release.apk}"

# The target is the data/ THE SERVER SERVES, never the one under the current
# directory: every worktree has its own empty data/, and a repo-relative path
# would "succeed" into a directory nothing serves. The running binary always
# uses VPSM_HOME as its DataDir.
VPSM_HOME="${VPSM_HOME:-/opt/panel}"
[ -d "$VPSM_HOME/data" ] || fail_early "VPSM_HOME=$VPSM_HOME has no data/; point VPSM_HOME at the served installation"
FDROID_REPO_DIR="${FDROID_REPO_DIR:-$VPSM_HOME/data/fdroid/repo}"
UPDATES_DIR="${UPDATES_DIR:-$VPSM_HOME/data/android-updates}"
WINDOW="${JANELA:-5}"
APKSIGNER="${APKSIGNER:-apksigner}"

fail() { echo "ERROR: $*" >&2; exit 1; }

[ -f "$APK" ] || fail "APK not found: $APK"

default_package_id() {
  sed -n 's/^vpsmanager\.applicationId=//p' "$ROOT_DIR/android/gradle.properties" | head -1 | tr -d '[:space:]'
}
PACKAGE_ID="${PACKAGE_ID:-$(default_package_id)}"
[ -n "$PACKAGE_ID" ] || fail "PACKAGE_ID is empty"

# An UNSIGNED APK is the expensive silent mistake here: Gradle produces
# `app-release-unsigned.apk` when the dev-key property is missing, and the device
# only refuses it after a full download.
if command -v "$APKSIGNER" >/dev/null 2>&1; then
  "$APKSIGNER" verify "$APK" >/dev/null 2>&1 || fail "the APK is not signed (or the signature is invalid): $APK"
else
  echo "warning: apksigner missing; signature not verified" >&2
fi

mkdir -p "$FDROID_REPO_DIR"

# The FILE NAME and the versionName differ on purpose. The dev build appends
# "-devsigned" to the versionName so a throwaway-key artifact identifies itself
# everywhere, but the public file name is `vpsm-<version>.apk` because it becomes
# a link that must keep its shape.
#
# The versionName comes from the APK itself, never from the argument: the app
# compares the index with what is installed, and an index announcing "0.1.24"
# for an APK named "0.1.24-devsigned" would make the update banner lie.
FILE_NAME="vpsm-$VERSION_NAME.apk"
REAL_VERSION_NAME="$VERSION_NAME"
AAPT2="${AAPT2:-$(command -v aapt2 || ls /opt/android-sdk/build-tools/*/aapt2 2>/dev/null | sort -r | head -1)}"
if [ -n "${AAPT2:-}" ] && [ -x "$AAPT2" ]; then
  read_name="$("$AAPT2" dump badging "$APK" 2>/dev/null | sed -n "s/.*versionName='\([^']*\)'.*/\1/p" | head -1)"
  [ -n "$read_name" ] && REAL_VERSION_NAME="$read_name"
  read_code="$("$AAPT2" dump badging "$APK" 2>/dev/null | sed -n "s/.*versionCode='\([^']*\)'.*/\1/p" | head -1)"
  if [ -n "$read_code" ] && [ "$read_code" != "$VERSION_CODE" ]; then
    fail "the APK has versionCode=$read_code but you asked for $VERSION_CODE; publishing it would make the channel lie"
  fi
else
  echo "warning: aapt2 missing; versionName not checked against the APK" >&2
fi

python3 - "$FDROID_REPO_DIR" "$UPDATES_DIR" "$PACKAGE_ID" "$APK" \
         "$FILE_NAME" "$REAL_VERSION_NAME" "$VERSION_CODE" "$WINDOW" <<'PY'
# -*- coding: utf-8 -*-
"""Updates the version registry and rewrites the index from it."""
import hashlib, json, io, os, sys

repo, updates, pkg, apk, name, ver, code, window = sys.argv[1:9]
code, window = int(code), int(window)

def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for block in iter(lambda: fh.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()

# The registry is the memory of the versions, independent of what is on disk.
# Without it every publication would drop the patch window together with the
# old binaries. The file name is kept as is: it is persisted in the repo dir.
registry_path = os.path.join(repo, "versoes.json")
registry = []
if os.path.exists(registry_path):
    with io.open(registry_path, encoding="utf-8") as fh:
        registry = json.load(fh)

# First run: seed the registry from the incremental channel, which already
# archived the previous APKs and names each base in its manifest, so the first
# update through this path is not a needless full download.
if not registry:
    manifest_path = os.path.join(updates, "manifest.json")
    if os.path.exists(manifest_path):
        with io.open(manifest_path, encoding="utf-8") as fh:
            m = json.load(fh)
        seen = {}
        target = m.get("latest", {})
        if target.get("sha256"):
            seen[int(target["version_code"])] = (target["version_name"], target["sha256"], int(target.get("size_bytes") or 0))
        for p in m.get("patches", []):
            if p.get("from_sha256") and p.get("from_version_code"):
                c = int(p["from_version_code"])
                archived = os.path.join(updates, "apks", p["from_sha256"] + ".apk")
                size = os.path.getsize(archived) if os.path.exists(archived) else 0
                seen.setdefault(c, (p.get("from_version_name", ""), p["from_sha256"], size))
        for c, (n, s, t) in seen.items():
            registry.append({"version_code": c, "version_name": n, "sha256": s,
                             "size_bytes": t, "file": "vpsm-%s.apk" % n})
        if registry:
            sys.stderr.write("registry seeded with %d version(s) from the incremental channel\n" % len(registry))

new = {
    "version_code": code,
    "version_name": ver,
    "sha256": sha256(apk),
    "size_bytes": os.path.getsize(apk),
    "file": name,
}
registry = [v for v in registry if int(v["version_code"]) != code] + [new]
registry.sort(key=lambda v: int(v["version_code"]), reverse=True)
registry = registry[:window]

# Public disk: only the new version. Older bytes stay in updates/apks/ (by
# hash), which is where android-patches.sh reads them.
import shutil
dest = os.path.join(repo, name)
if os.path.abspath(apk) != os.path.abspath(dest):
    shutil.copy2(apk, dest)
os.chmod(dest, 0o644)
for f in os.listdir(repo):
    if f.startswith("vpsm-") and f.endswith(".apk") and f != name:
        os.remove(os.path.join(repo, f))

with io.open(registry_path, "w", encoding="utf-8") as fh:
    json.dump(registry, fh, indent=2, ensure_ascii=False)

versions = {}
for v in registry:
    versions[v["sha256"]] = {
        "manifest": {"versionCode": int(v["version_code"]), "versionName": v["version_name"]},
        "file": {"name": "/" + v["file"], "sha256": v["sha256"], "size": int(v["size_bytes"])},
    }
idx = {"repo": {"name": "vps-manager"}, "packages": {pkg: {"versions": versions}}}
with io.open(os.path.join(repo, "index-v2.json"), "w", encoding="utf-8") as fh:
    json.dump(idx, fh, indent=2)

print("published %s · sha %s · window of %d version(s)" % (name, new["sha256"][:16], len(registry)))
PY

# The other half: the channel the APP checks. Without it the app never learns
# about the new version.
echo "── generating the incremental channel (android-patches.sh) ──"
ANDROID_UPDATES_DIR="$UPDATES_DIR" FDROID_REPO_DIR="$FDROID_REPO_DIR" \
  PACKAGE_ID="$PACKAGE_ID" PATCH_WINDOW="$WINDOW" \
  "$ROOT_DIR/scripts/android-patches.sh"

echo "done: $REAL_VERSION_NAME ($VERSION_CODE) is in the repository AND in the app channel"
