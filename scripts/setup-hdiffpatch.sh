#!/usr/bin/env bash
set -euo pipefail

VERSION="v5.1.3"
ARCHIVE="hdiffpatch_${VERSION}_bin_linux64.zip"
URL="https://github.com/sisong/HDiffPatch/releases/download/${VERSION}/${ARCHIVE}"
EXPECTED_SHA256="628963bf2ee9108a97260fa5eef44acd9ec94369b76090a957c9182b3abbb558"
DEST="${DESTINATION:-/usr/local/bin}"

fail() { echo "ERROR: $*" >&2; exit 1; }

version_of() { { "$1" 2>&1 || true; } | head -1; }

if command -v hdiffz >/dev/null 2>&1 && command -v hpatchz >/dev/null 2>&1; then
  installed="$(version_of hdiffz)"
  case "$installed" in
    *"${VERSION#v}"*)
      echo "already provisioned: $installed"
      exit 0
      ;;
  esac
  echo "WARNING: hdiffz present but a different version ($installed); reinstalling ${VERSION}"
fi

command -v curl >/dev/null 2>&1 || fail "curl not found"
command -v unzip >/dev/null 2>&1 || fail "unzip not found"
command -v sha256sum >/dev/null 2>&1 || fail "sha256sum not found"

TMP="$(mktemp -d "${TMPDIR:-/tmp}/panel-hdiffpatch.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

echo "==> downloading ${ARCHIVE}"
curl -fsSL -o "$TMP/$ARCHIVE" "$URL" || fail "download failed: $URL"

got="$(sha256sum "$TMP/$ARCHIVE" | cut -d' ' -f1)"
[ "$got" = "$EXPECTED_SHA256" ] \
  || fail "download SHA-256 does NOT match: expected $EXPECTED_SHA256, got $got. Nothing was installed."
echo "OK: SHA-256 matches"

unzip -q -o "$TMP/$ARCHIVE" -d "$TMP/extracted"
for bin in hdiffz hpatchz; do
  src="$TMP/extracted/linux64/$bin"
  [ -f "$src" ] || fail "$bin is missing from the package"
  install -m 0755 "$src" "$DEST/$bin"
done

echo "OK: $(version_of "$DEST/hdiffz") and $(version_of "$DEST/hpatchz") installed in $DEST"
