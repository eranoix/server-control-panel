#!/usr/bin/env bash
# test-android-publish.sh: covers the three behaviours of
# scripts/android-publish.sh with fully throwaway material (a keystore created
# and deleted in this run, never the project's real keystore).
#
# Test 1: fingerprint mismatch -> the script refuses, nothing is published.
# Test 2: fingerprint matches + index bundle present -> publishes the APK and
#         the index; index-v2.json references the exact APK name.
# Test 3: no temporary file with key material survives either exit path, and
#         the script never touches data/secrets.vault (the repokey must not be
#         reachable from it, see docs/android-fdroid-repo.md).
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT/scripts/android-publish.sh"
APKSIGNER_BIN="$(command -v apksigner || true)"
AAPT2_BIN=""
ANDROID_JAR=""
for c in /opt/android-sdk/build-tools/*/apksigner; do
  [ -z "$APKSIGNER_BIN" ] && [ -x "$c" ] && APKSIGNER_BIN="$c"
done
for c in /opt/android-sdk/build-tools/*/aapt2; do
  [ -z "$AAPT2_BIN" ] && [ -x "$c" ] && AAPT2_BIN="$c"
done
ANDROID_JAR="$(find /opt/android-sdk/platforms -name android.jar 2>/dev/null | sort -V | tail -1)"

[ -f "$SCRIPT" ] || { echo "cannot find $SCRIPT"; exit 2; }
[ -n "$APKSIGNER_BIN" ] || { echo "apksigner not found; cannot run the test"; exit 2; }
[ -n "$AAPT2_BIN" ] || { echo "aapt2 not found; cannot run the test"; exit 2; }
[ -n "$ANDROID_JAR" ] || { echo "android.jar not found; cannot run the test"; exit 2; }
for bin in keytool java python3; do
  command -v "$bin" >/dev/null 2>&1 || { echo "$bin not found; cannot run the test"; exit 2; }
done

pass=0; fail=0
ok() { echo "  OK: $1"; pass=$((pass+1)); }
no() { echo "  FAILED: $1"; fail=$((fail+1)); }
echo "=== test-android-publish ==="

TMP="$(mktemp -d "${TMPDIR:-/tmp}/vpsm-android-publish.XXXXXX")" || exit 2
trap 'case "$TMP" in "${TMPDIR:-/tmp}"/vpsm-android-publish.*) rm -rf "$TMP";; esac' EXIT

# Fixtures: a real throwaway keystore (same parameters as the drill in
# docs/android-signing-keystore.md, but with a short validity).
export STOREPASS_OK="$(openssl rand -base64 24)"
export STOREPASS_BAD="$(openssl rand -base64 24)"

make_signed_apk() {
  local workdir="$1" storepass_var="$2" out_apk="$3"
  local ks="$workdir/ks.jks"
  keytool -genkeypair -v -keystore "$ks" -alias throwaway \
    -keyalg RSA -keysize 2048 -validity 1 -storetype PKCS12 \
    -storepass:env "$storepass_var" -keypass:env "$storepass_var" \
    -dname "CN=throwaway-test" >/dev/null 2>&1

  # A real APK with a valid binary AndroidManifest.xml (via aapt2 link): without
  # it apksigner cannot determine minSdkVersion and refuses to verify.
  local manifest="$workdir/AndroidManifest.xml"
  cat > "$manifest" <<'EOF'
<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="br.tech.vpsmanager.app.fixture">
    <uses-sdk android:minSdkVersion="21" android:targetSdkVersion="34" />
</manifest>
EOF
  local raw_apk="$workdir/raw.apk"
  "$AAPT2_BIN" link -o "$raw_apk" -I "$ANDROID_JAR" --manifest "$manifest" >/dev/null 2>&1

  "$APKSIGNER_BIN" sign --ks "$ks" \
    --ks-pass "env:$storepass_var" --ks-key-alias throwaway \
    --out "$out_apk" "$raw_apk" >/dev/null 2>&1

  "$APKSIGNER_BIN" verify --print-certs "$out_apk" 2>/dev/null \
    | grep -m1 'certificate SHA-256 digest:' \
    | sed -E 's/.*digest:[[:space:]]*//'
}

WORK_OK="$TMP/gen-ok"; mkdir -p "$WORK_OK"
WORK_BAD="$TMP/gen-bad"; mkdir -p "$WORK_BAD"

APK_OK="$TMP/app-release-signed-ok.apk"
FP_OK_RAW="$(make_signed_apk "$WORK_OK" STOREPASS_OK "$APK_OK")"
APK_BAD="$TMP/app-release-signed-bad.apk"
FP_BAD_RAW="$(make_signed_apk "$WORK_BAD" STOREPASS_BAD "$APK_BAD")"

if [ -z "$FP_OK_RAW" ] || [ -z "$FP_BAD_RAW" ] || [ "$FP_OK_RAW" = "$FP_BAD_RAW" ]; then
  echo "could not generate two test APKs with distinct fingerprints; aborting"
  exit 2
fi

# Keystore doc fixture: records only the "OK" fingerprint as the reference,
# in keytool's colon upper-case format.
FP_OK_COLON="$(echo "$FP_OK_RAW" | fold -w2 | paste -sd: | tr '[:lower:]' '[:upper:]')"
KEYSTORE_DOC="$TMP/android-signing-keystore.md"
cat > "$KEYSTORE_DOC" <<EOF
## 5. Fingerprint

\`\`\`
SHA-256: $FP_OK_COLON
\`\`\`
EOF

echo "--- Test 1: fingerprint mismatch ---"
T1_STAGING="$TMP/t1-staging"
T1_REPO="$TMP/t1-repo"
mkdir -p "$T1_STAGING/77" "$T1_REPO"
cp "$APK_BAD" "$T1_STAGING/77/app-release-signed.apk"

if STAGING_DIR="$T1_STAGING" FDROID_REPO_DIR="$T1_REPO" KEYSTORE_DOC="$KEYSTORE_DOC" \
   APKSIGNER="$APKSIGNER_BIN" "$SCRIPT" 77 >"$TMP/t1.out" 2>&1; then
  no "the script should have exited with an error on a fingerprint mismatch"
else
  ok "the script exited with an error (exit != 0) on a fingerprint mismatch"
fi
grep -qi "fingerprint" "$TMP/t1.out" && ok "the error message mentions the fingerprint" || no "the error message does not mention the fingerprint ($(cat "$TMP/t1.out"))"
if [ -z "$(find "$T1_REPO" -mindepth 1 2>/dev/null)" ]; then
  ok "nothing was copied into the served repository"
else
  no "something was copied into the repository despite the fingerprint mismatch: $(ls -la "$T1_REPO")"
fi

echo "--- Test 2: fingerprint matches + index bundle ---"
T2_STAGING="$TMP/t2-staging"
T2_REPO="$TMP/t2-repo"
mkdir -p "$T2_STAGING/101/fdroid-repo" "$T2_REPO"
cp "$APK_OK" "$T2_STAGING/101/app-release-signed.apk"
cp "$APK_OK" "$T2_STAGING/101/fdroid-repo/app-release-signed.apk"
cat > "$T2_STAGING/101/fdroid-repo/index-v2.json" <<'EOF'
{"packages": {"br.tech.vpsmanager.app": {"versions": {"app-release-signed.apk": {}}}}}
EOF

if STAGING_DIR="$T2_STAGING" FDROID_REPO_DIR="$T2_REPO" KEYSTORE_DOC="$KEYSTORE_DOC" \
   APKSIGNER="$APKSIGNER_BIN" "$SCRIPT" 101 >"$TMP/t2.out" 2>&1; then
  ok "the script published successfully with a matching fingerprint"
else
  no "the script failed with a matching fingerprint: $(cat "$TMP/t2.out")"
fi
[ -f "$T2_REPO/app-release-signed.apk" ] && ok "the APK was copied into the served repository" \
  || no "the APK did not appear in $T2_REPO"
if [ -f "$T2_REPO/index-v2.json" ] && grep -q "app-release-signed.apk" "$T2_REPO/index-v2.json"; then
  ok "the published index-v2.json references the exact APK name"
else
  no "the published index-v2.json does not reference the APK"
fi

echo "--- Test 3: no key material survives, the script never touches the vault ---"
# Checks for a real INVOCATION, not the explanatory comments (which mention
# data/secrets.vault to say it is NOT used).
if grep -qE "vpsmctl secrets get|fdroid_repo_keystore_b64|fdroid_repo_keystore_pass" "$SCRIPT"; then
  no "android-publish.sh invokes the vault/repokey and must not (docs/android-fdroid-repo.md §1-2)"
else
  ok "android-publish.sh never invokes data/secrets.vault or the repokey"
fi
LEFTOVER="$(find "${TMPDIR:-/tmp}" -maxdepth 1 -iname "*repokey*" -o -iname "*fdroid_repo_keystore*" 2>/dev/null)"
if [ -z "$LEFTOVER" ]; then
  ok "no temporary repokey file left in ${TMPDIR:-/tmp}"
else
  no "suspicious temporary file left: $LEFTOVER"
fi

echo ""
echo "=== result: $pass ok, $fail failed ==="
[ "$fail" -eq 0 ]
