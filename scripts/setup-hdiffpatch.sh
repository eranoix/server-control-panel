#!/usr/bin/env bash
# setup-hdiffpatch.sh: installs hdiffz/hpatchz (HDiffPatch, MIT license) on this
# server. Idempotent: safe to run again at any time.
#
# hdiffz produces the binary patches between signed APKs (scripts/android-patches.sh).
# hpatchz applies them; on the device that is libhpatchz.so, but having it here
# lets the server VERIFY that a patch rebuilds bytes identical to the signed APK
# before publishing it.
#
# Official static binary rather than a source build: the source build needs
# submodules (lzma, zstd, libmd5) the GitHub tarball lacks, and the pinned
# SHA-256 below guarantees the download is what was audited.
# When upgrading, change VERSION and EXPECTED_SHA256 together and regenerate the
# patches, since the output format may change between major versions.
set -euo pipefail

VERSION="v5.1.3"
ARCHIVE="hdiffpatch_${VERSION}_bin_linux64.zip"
URL="https://github.com/sisong/HDiffPatch/releases/download/${VERSION}/${ARCHIVE}"
EXPECTED_SHA256="628963bf2ee9108a97260fa5eef44acd9ec94369b76090a957c9182b3abbb558"
# DESTINO is the install-dir override read from the environment.
DEST="${DESTINO:-/usr/local/bin}"

fail() { echo "ERROR: $*" >&2; exit 1; }

# hdiffz/hpatchz with no arguments print the usage banner and exit non-zero,
# which would abort under `set -e -o pipefail`; hence the `|| true`.
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

TMP="$(mktemp -d "${TMPDIR:-/tmp}/vpsm-hdiffpatch.XXXXXX")"
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
