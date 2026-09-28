#!/usr/bin/env bash
set -euo pipefail

VERSION_CODE="${1:?usage: scripts/android-publish.sh <versionCode>}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STAGING_DIR="${STAGING_DIR:-$ROOT_DIR/data/android-release-staging}"
FDROID_REPO_DIR="${FDROID_REPO_DIR:-$ROOT_DIR/data/fdroid/repo}"
KEYSTORE_DOC="${KEYSTORE_DOC:-$ROOT_DIR/docs/android-signing-keystore.md}"
APKSIGNER="${APKSIGNER:-apksigner}"

"$ROOT_DIR/scripts/release-git.sh" check

RELEASE_DIR="$STAGING_DIR/$VERSION_CODE"
SIGNED_APK="$RELEASE_DIR/app-release-signed.apk"
REPO_BUNDLE="$RELEASE_DIR/fdroid-repo"

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

normalize_fp() {
  tr -d ':[:space:]' <<<"$1" | tr '[:upper:]' '[:lower:]'
}

[ -f "$SIGNED_APK" ] || fail "signed APK not found at $SIGNED_APK; do the offline signing first"

EXPECTED_LINE=$(grep -m1 '^SHA-256:' "$KEYSTORE_DOC" || true)
[ -n "$EXPECTED_LINE" ] || fail "no 'SHA-256:' line found in $KEYSTORE_DOC"
EXPECTED_RAW=$(sed -E 's/^SHA-256:[[:space:]]*//' <<<"$EXPECTED_LINE")
case "$EXPECTED_RAW" in
  *PENDING*) fail "the fingerprint in $KEYSTORE_DOC is still PENDING: the release keystore has not been generated yet" ;;
esac
EXPECTED_FP="$(normalize_fp "$EXPECTED_RAW")"

ACTUAL_LINE=$("$APKSIGNER" verify --print-certs "$SIGNED_APK" 2>&1 | grep -m1 'certificate SHA-256 digest:') \
  || fail "'apksigner verify' failed for $SIGNED_APK (invalid or corrupt signature, or apksigner not found)"
ACTUAL_RAW=$(sed -E 's/.*certificate SHA-256 digest:[[:space:]]*//' <<<"$ACTUAL_LINE")
ACTUAL_FP="$(normalize_fp "$ACTUAL_RAW")"

[ -n "$ACTUAL_FP" ] || fail "could not extract the SHA-256 fingerprint from the apksigner output"

if [ "$ACTUAL_FP" != "$EXPECTED_FP" ]; then
  fail "fingerprint does NOT match: expected $EXPECTED_FP, got $ACTUAL_FP. APK rejected, nothing was published."
fi

echo "OK: APK fingerprint matches $KEYSTORE_DOC (versionCode=$VERSION_CODE)"

[ -d "$REPO_BUNDLE" ] || fail "signed repository bundle not found at $REPO_BUNDLE; run 'fdroid update' offline on the operator machine (docs/android-fdroid-repo.md §6) and upload the result here before publishing"

INDEX_JSON="$REPO_BUNDLE/index-v2.json"
[ -f "$INDEX_JSON" ] || fail "$REPO_BUNDLE has no index-v2.json: incomplete repository bundle"

APK_BASENAME="$(basename "$SIGNED_APK")"
BUNDLED_APK="$REPO_BUNDLE/$APK_BASENAME"
[ -f "$BUNDLED_APK" ] || fail "$REPO_BUNDLE does not contain $APK_BASENAME: the index was generated without this APK"

if ! grep -q "$APK_BASENAME" "$INDEX_JSON"; then
  fail "index-v2.json in $REPO_BUNDLE does not reference $APK_BASENAME: stale index, or generated before this APK"
fi
echo "OK: index-v2.json references $APK_BASENAME"

mkdir -p "$FDROID_REPO_DIR"
rsync -a --delete-after "$REPO_BUNDLE"/ "$FDROID_REPO_DIR"/

echo "OK: F-Droid repository published at $FDROID_REPO_DIR (versionCode=$VERSION_CODE)"

if [ "${SKIP_PATCHES:-0}" != "1" ]; then
  if "$ROOT_DIR/scripts/android-patches.sh"; then
    :
  else
    echo "WARNING: incremental patch generation failed. The F-Droid release is published and intact, but the incremental update channel is stale. Run scripts/android-patches.sh by hand." >&2
  fi
fi

AAPT2="${AAPT2:-$(command -v aapt2 || ls /opt/android-sdk/build-tools/*/aapt2 2>/dev/null | sort -r | head -1)}"
VERSION_NAME="$("$AAPT2" dump badging "$SIGNED_APK" 2>/dev/null | sed -n "s/.*versionName='\([^']*\)'.*/\1/p" | head -1)"
if [ "${SKIP_GIT_RELEASE:-0}" != "1" ]; then
  [ -n "$VERSION_NAME" ] || fail "could not read versionName from $SIGNED_APK with aapt2"
  "$ROOT_DIR/scripts/release-git.sh" publish "$VERSION_NAME"
fi
