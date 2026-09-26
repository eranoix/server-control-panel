#!/usr/bin/env bash
# Gera as fixtures do teste de CICLO REAL (ApkPatcherRealApkTest): dois APKs
# de release assinados com a chave de desenvolvimento e o patch HDiffPatch
# entre eles, em build/patch-fixtures/ (registrado como srcDir de assets do
# androidTest em build.gradle.kts).
#
# POR QUE ISTO NAO E VERSIONADO
# O APK de release deste projeto tem ~33 MB. O par mais o patch daria ~66 MB
# de binario opaco no repositorio, para sempre, para provar algo que este
# script reproduz em ~4 minutos. Nao se paga -- ainda mais num repositorio que
# e a ferramenta de trabalho diaria de alguem. O que fica versionado e o par
# PEQUENO (270 KB, tools/make-smoke-fixtures.sh), que cobre carga da .so,
# assinatura JNI e conferencia de hash num checkout limpo.
#
# Consequencia honesta: sem rodar este script antes, ApkPatcherRealApkTest
# FALHA -- nao "pula". Teste que se ausenta em silencio nao prova nada, e a
# suite ficar verde sem a prova central seria pior que ficar vermelha.
#
# Uso:  ./tools/make-patch-fixtures.sh
# Env:  KEYSTORE=<jks>  HDIFFZ=<binario>  ABI=<x86_64|arm64-v8a>

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODULE_DIR="$(dirname "$SCRIPT_DIR")"
ANDROID_DIR="$(dirname "$MODULE_DIR")"
OUT_DIR="$MODULE_DIR/build/patch-fixtures/patch-fixtures"
STAGE_DIR="$MODULE_DIR/.build/apks"

# x86_64 por padrao porque o alvo de teste e o emulador do Lab
# (docs/android-emulador-lab.md). A ENTREGA e arm64-v8a; o ciclo aqui e o
# mesmo nas duas ABIs -- o que muda e qual .so vai dentro do APK, e o APK e
# so um saco de bytes do ponto de vista do patcher.
ABI="${ABI:-x86_64}"
KEYSTORE="${KEYSTORE:-/opt/panel/data/android-dev-signing/vpsmanager-DEV-NAO-E-RELEASE.jks}"

OLD_VERSION_NAME="${OLD_VERSION_NAME:-0.1.5}"
OLD_VERSION_CODE="${OLD_VERSION_CODE:-105}"
NEW_VERSION_NAME="${NEW_VERSION_NAME:-0.1.6}"
NEW_VERSION_CODE="${NEW_VERSION_CODE:-106}"

HDIFFZ="${HDIFFZ:-$MODULE_DIR/.build/hdiffz}"
if [ ! -x "$HDIFFZ" ]; then
  HDIFFZ="$(command -v hdiffz || true)"
fi
if [ -z "$HDIFFZ" ] || [ ! -x "$HDIFFZ" ]; then
  echo "make-patch-fixtures: hdiffz nao encontrado (ver tools/make-smoke-fixtures.sh para como construir)" >&2
  exit 1
fi
if [ ! -f "$KEYSTORE" ]; then
  echo "make-patch-fixtures: keystore de desenvolvimento ausente: $KEYSTORE" >&2
  echo "  (docs/android-chave-dev.md -- NAO e a chave de release, que nunca toca esta maquina)" >&2
  exit 1
fi

mkdir -p "$STAGE_DIR" "$OUT_DIR"

build_apk() {
  local version_name="$1" version_code="$2" dest="$3"
  echo "make-patch-fixtures: build $version_name ($version_code) abi=$ABI" >&2
  ( cd "$ANDROID_DIR" && ./gradlew :app:assembleRelease \
      "-PversionName=$version_name" \
      "-PversionCode=$version_code" \
      "-Pvpsmanager.devKeystore=$KEYSTORE" \
      "-Pvpsmanager.abi=$ABI" >/dev/null )
  cp -f "$ANDROID_DIR/app/build/outputs/apk/release/app-release.apk" "$dest"
}

build_apk "$OLD_VERSION_NAME" "$OLD_VERSION_CODE" "$STAGE_DIR/old.apk"
build_apk "$NEW_VERSION_NAME" "$NEW_VERSION_CODE" "$STAGE_DIR/new.apk"

# Mesmas opcoes do gerador do servidor. -s-4m mantem o pico de memoria do
# PATCH baixo (modo de fluxo), que e o que importa no celular; -c-zstd e o
# compressor padrao do hdiffz e o unico caminho que o .so deste modulo
# realmente vai encontrar em producao.
echo "make-patch-fixtures: hdiffz -s-4m -c-zstd-21-24" >&2
"$HDIFFZ" -s-4m -c-zstd-21-24 -f "$STAGE_DIR/old.apk" "$STAGE_DIR/new.apk" "$STAGE_DIR/update.hdiff" >/dev/null

cp -f "$STAGE_DIR/old.apk"      "$OUT_DIR/base.apk"
cp -f "$STAGE_DIR/update.hdiff" "$OUT_DIR/update.hdiff"

# O APK NOVO nao e copiado para os assets de proposito: o teste nao precisa
# dele, precisa do que ele deveria ser. Levar 33 MB a mais para dentro do APK
# de teste so para comparar arquivo com arquivo seria desperdicio -- o
# SHA-256 e a afirmacao inteira.
NEW_SHA="$(sha256sum "$STAGE_DIR/new.apk" | cut -d' ' -f1)"
NEW_SIZE="$(stat -c%s "$STAGE_DIR/new.apk")"
OLD_SHA="$(sha256sum "$STAGE_DIR/old.apk" | cut -d' ' -f1)"

cat > "$OUT_DIR/expected.properties" <<EOF
# Gerado por tools/make-patch-fixtures.sh -- nao editar a mao.
# Descreve o APK que aplicar update.hdiff sobre base.apk DEVE produzir.
abi=$ABI
oldVersionName=$OLD_VERSION_NAME
oldVersionCode=$OLD_VERSION_CODE
oldSha256=$OLD_SHA
newVersionName=$NEW_VERSION_NAME
newVersionCode=$NEW_VERSION_CODE
newSha256=$NEW_SHA
newSizeBytes=$NEW_SIZE
patchSizeBytes=$(stat -c%s "$STAGE_DIR/update.hdiff")
baseSizeBytes=$(stat -c%s "$STAGE_DIR/old.apk")
EOF

echo >&2
cat "$OUT_DIR/expected.properties" >&2
echo >&2
echo "make-patch-fixtures: pronto em $OUT_DIR" >&2
