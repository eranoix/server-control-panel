#!/usr/bin/env bash

set -euo pipefail

ALIAS="servercontrolpanel"

for bin in keytool openssl; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "ERROR: $bin not found in PATH; cannot run the drill" >&2
    exit 1
  fi
done

WORKDIR="$(mktemp -d -t android-keystore-drill.XXXXXX)"
trap 'rm -rf "$WORKDIR"' EXIT

echo "== throwaway keystore drill, temp dir: $WORKDIR =="

STORE_PASS="$(openssl rand -base64 24)"
KEY_PASS="$(openssl rand -base64 24)"
ENC_PASS="$(openssl rand -base64 24)"
export STORE_PASS KEY_PASS ENC_PASS

KEYSTORE="$WORKDIR/drill-keystore.jks"
BACKUP_ENC="$WORKDIR/drill-keystore.jks.enc"
RESTORE_DIR="$WORKDIR/restore"
mkdir -p "$RESTORE_DIR"
RESTORED_KEYSTORE="$RESTORE_DIR/drill-keystore.jks"

echo "== generating a throwaway keystore (same parameters as runbook section 3) =="
keytool -genkeypair -v \
  -keystore "$KEYSTORE" \
  -alias "$ALIAS" \
  -keyalg RSA \
  -keysize 4096 \
  -validity 10000 \
  -storetype PKCS12 \
  -storepass:env STORE_PASS \
  -keypass:env KEY_PASS \
  -dname "CN=drill, OU=drill, O=drill, L=drill, ST=drill, C=BR" \
  >/dev/null

extract_sha256() {
  local ks="$1"
  keytool -list -v -keystore "$ks" -alias "$ALIAS" -storepass:env STORE_PASS \
    | grep "SHA256:" | head -1 \
    | sed -E 's/^[[:space:]]*SHA256:[[:space:]]*//' \
    | tr '[:lower:]' '[:upper:]'
}

FP_ORIGINAL="$(extract_sha256 "$KEYSTORE")"
echo "original fingerprint : $FP_ORIGINAL"

echo "== encrypting the backup (openssl aes-256-cbc, pbkdf2, throwaway password) =="
openssl enc -aes-256-cbc -pbkdf2 -salt -pass env:ENC_PASS \
  -in "$KEYSTORE" -out "$BACKUP_ENC"

echo "== restoring the encrypted backup into another directory =="
openssl enc -d -aes-256-cbc -pbkdf2 -pass env:ENC_PASS \
  -in "$BACKUP_ENC" -out "$RESTORED_KEYSTORE"

FP_RESTORED="$(extract_sha256 "$RESTORED_KEYSTORE")"
echo "restored fingerprint : $FP_RESTORED"

if [ -z "$FP_ORIGINAL" ] || [ -z "$FP_RESTORED" ]; then
  echo "ERROR: could not extract a valid SHA-256 fingerprint" >&2
  exit 1
fi

if [ "$FP_ORIGINAL" != "$FP_RESTORED" ]; then
  echo "ERROR: fingerprint differs between original and restored copy; drill FAILED" >&2
  exit 1
fi

echo "OK: generation + encrypted backup + restore + fingerprint check all match."
echo "(fully throwaway: the temp dir is removed on exit)"
