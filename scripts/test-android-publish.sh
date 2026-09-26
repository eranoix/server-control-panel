#!/usr/bin/env bash
# test-android-publish.sh — cobre os três comportamentos de
# scripts/android-publish.sh (TDD, plano 12-03 Task 3) usando material
# inteiramente descartável: um keystore gerado e apagado nesta mesma
# execução, nunca o keystore real do projeto.
#
# Teste 1: fingerprint divergente -> script recusa, nada é publicado.
# Teste 2: fingerprint bate + pacote de índice presente -> publica a APK e
#          o índice, index-v2.json referencia o nome exato da APK.
# Teste 3: nenhum arquivo temporário com material de chave sobrevive a
#          nenhum dos dois caminhos de saída (e o script nunca referencia
#          data/secrets.vault — a repokey nunca deveria estar acessível
#          para ele em primeiro lugar, ver docs/android-fdroid-repo.md).
set -uo pipefail

RAIZ="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$RAIZ/scripts/android-publish.sh"
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

[ -f "$SCRIPT" ] || { echo "não achei $SCRIPT"; exit 2; }
[ -n "$APKSIGNER_BIN" ] || { echo "apksigner não encontrado — não dá para rodar o teste"; exit 2; }
[ -n "$AAPT2_BIN" ] || { echo "aapt2 não encontrado — não dá para rodar o teste"; exit 2; }
[ -n "$ANDROID_JAR" ] || { echo "android.jar não encontrado — não dá para rodar o teste"; exit 2; }
for bin in keytool java python3; do
  command -v "$bin" >/dev/null 2>&1 || { echo "$bin não encontrado — não dá para rodar o teste"; exit 2; }
done

pass=0; fail=0
ok() { echo "  OK: $1"; pass=$((pass+1)); }
no() { echo "  FALHOU: $1"; fail=$((fail+1)); }
echo "=== test-android-publish (12-03 Task 3) ==="

TMP="$(mktemp -d "${TMPDIR:-/tmp}/vpsm-android-publish.XXXXXX")" || exit 2
trap 'case "$TMP" in "${TMPDIR:-/tmp}"/vpsm-android-publish.*) rm -rf "$TMP";; esac' EXIT

# ── fixtures: keystore descartável real (mesmos parâmetros do drill de
#    docs/android-signing-keystore.md, mas com validade curta — é lixo) ────
export STOREPASS_OK="$(openssl rand -base64 24)"
export STOREPASS_BAD="$(openssl rand -base64 24)"

make_signed_apk() {
  local workdir="$1" storepass_var="$2" out_apk="$3"
  local ks="$workdir/ks.jks"
  keytool -genkeypair -v -keystore "$ks" -alias descartavel \
    -keyalg RSA -keysize 2048 -validity 1 -storetype PKCS12 \
    -storepass:env "$storepass_var" -keypass:env "$storepass_var" \
    -dname "CN=teste-descartavel" >/dev/null 2>&1

  # APK real com AndroidManifest.xml binário válido (via aapt2 link) — sem
  # isso o apksigner não consegue determinar o minSdkVersion e recusa a
  # verificação, o que não reflete o caminho real de produção (APK real vem
  # do próprio `gradlew assembleRelease`, que sempre gera um manifesto
  # binário válido).
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
    --ks-pass "env:$storepass_var" --ks-key-alias descartavel \
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
  echo "não consegui gerar duas APKs de teste com fingerprints distintos — abortando"
  exit 2
fi

# docs/android-signing-keystore.md fixture — registra apenas o fingerprint
# "OK" como o valor de referência, formato colon-uppercase (keytool).
FP_OK_COLON="$(echo "$FP_OK_RAW" | fold -w2 | paste -sd: | tr '[:lower:]' '[:upper:]')"
KEYSTORE_DOC="$TMP/android-signing-keystore.md"
cat > "$KEYSTORE_DOC" <<EOF
## 5. Fingerprint

\`\`\`
SHA-256: $FP_OK_COLON
\`\`\`
EOF

# ─────────────────────────────────────────────────────────────────────────
echo "--- Teste 1: fingerprint divergente ---"
T1_STAGING="$TMP/t1-staging"
T1_REPO="$TMP/t1-repo"
mkdir -p "$T1_STAGING/77" "$T1_REPO"
cp "$APK_BAD" "$T1_STAGING/77/app-release-signed.apk"

if STAGING_DIR="$T1_STAGING" FDROID_REPO_DIR="$T1_REPO" KEYSTORE_DOC="$KEYSTORE_DOC" \
   APKSIGNER="$APKSIGNER_BIN" "$SCRIPT" 77 >"$TMP/t1.out" 2>&1; then
  no "script deveria ter saído com erro para fingerprint divergente"
else
  ok "script saiu com erro (exit != 0) para fingerprint divergente"
fi
grep -qi "fingerprint" "$TMP/t1.out" && ok "mensagem de erro menciona fingerprint" || no "mensagem de erro não menciona fingerprint ($(cat "$TMP/t1.out"))"
if [ -z "$(find "$T1_REPO" -mindepth 1 2>/dev/null)" ]; then
  ok "nada foi copiado para o repositório servido"
else
  no "algo foi copiado para o repositório mesmo com fingerprint divergente: $(ls -la "$T1_REPO")"
fi

# ─────────────────────────────────────────────────────────────────────────
echo "--- Teste 2: fingerprint bate + pacote de índice ---"
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
  ok "script publicou com sucesso para fingerprint correspondente"
else
  no "script falhou com fingerprint correspondente: $(cat "$TMP/t2.out")"
fi
[ -f "$T2_REPO/app-release-signed.apk" ] && ok "APK foi copiada para o repositório servido" \
  || no "APK não apareceu em $T2_REPO"
if [ -f "$T2_REPO/index-v2.json" ] && grep -q "app-release-signed.apk" "$T2_REPO/index-v2.json"; then
  ok "index-v2.json publicado referencia o nome exato da APK"
else
  no "index-v2.json publicado não referencia a APK"
fi

# ─────────────────────────────────────────────────────────────────────────
echo "--- Teste 3: nenhum material de chave sobrevive, script nunca toca o vault ---"
# Checagem por INVOCAÇÃO real (não pelo texto explicativo nos comentários,
# que legitimamente cita data/secrets.vault para dizer que NÃO é usado).
if grep -qE "vpsmctl secrets get|fdroid_repo_keystore_b64|fdroid_repo_keystore_pass" "$SCRIPT"; then
  no "android-publish.sh invoca o vault/repokey — não deveria (docs/android-fdroid-repo.md §1-2)"
else
  ok "android-publish.sh nunca invoca data/secrets.vault nem a repokey"
fi
LEFTOVER="$(find "${TMPDIR:-/tmp}" -maxdepth 1 -iname "*repokey*" -o -iname "*fdroid_repo_keystore*" 2>/dev/null)"
if [ -z "$LEFTOVER" ]; then
  ok "nenhum arquivo temporário de repokey sobrou em ${TMPDIR:-/tmp}"
else
  no "sobrou arquivo temporário suspeito: $LEFTOVER"
fi

echo ""
echo "=== resultado: $pass ok, $fail falhou ==="
[ "$fail" -eq 0 ]
