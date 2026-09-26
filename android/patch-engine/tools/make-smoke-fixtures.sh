#!/usr/bin/env bash
# Regera o par de fixtures PEQUENAS de src/androidTest/assets/hdiff/ --
# smoke-old.bin (256 KiB) e smoke.hdiff (~8 KiB), os unicos binarios de teste
# versionados deste modulo.
#
# Por que existem, se ja ha o teste com os APKs de verdade: aquele depende de
# tools/make-patch-fixtures.sh ter rodado antes (dois builds de :app, ~4 min,
# 66 MB fora do repositorio). Este par cabe em 270 KB, vai junto no clone e
# garante que `connectedAndroidTest` num checkout limpo ainda prove o
# essencial: que a .so carrega nesta ABI, que a assinatura JNI casa, que o
# patch aplica e que a conferencia de SHA-256 reprova o que tem de reprovar.
#
# Determinismo: a semente do PRNG e fixa, entao rodar de novo produz bytes
# identicos -- o .hdiff so muda se o hdiffz mudar de versao.
#
# Uso: ./tools/make-smoke-fixtures.sh   (de qualquer diretorio)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODULE_DIR="$(dirname "$SCRIPT_DIR")"
ASSETS_DIR="$MODULE_DIR/src/androidTest/assets/hdiff"
WORK_DIR="$MODULE_DIR/.build/smoke"

HDIFFZ="${HDIFFZ:-$MODULE_DIR/.build/hdiffz}"
if [ ! -x "$HDIFFZ" ]; then
  HDIFFZ="$(command -v hdiffz || true)"
fi
if [ -z "$HDIFFZ" ] || [ ! -x "$HDIFFZ" ]; then
  cat >&2 <<'MSG'
make-smoke-fixtures: hdiffz nao encontrado.

O GERADOR nao e vendorizado neste modulo de proposito -- so o patcher entra
no aparelho (ver src/main/cpp/Android.mk). Para construir um hdiffz local:

  clone https://github.com/sisong/HDiffPatch (e os irmaos lzma/ zstd/ xxHash/
  libmd5/, mesmos commits de toolchain.properties), depois dentro dele:
      make LDEF=0 ZLIB=2 BSD=0 BZIP2=0 VCD=0 DIR_DIFF=0 -j8
  e aponte:  HDIFFZ=<caminho>/hdiffz ./tools/make-smoke-fixtures.sh

Ou copie o binario para .build/hdiffz (ignorado pelo controle de versao).
MSG
  exit 1
fi

mkdir -p "$WORK_DIR" "$ASSETS_DIR"

python3 - "$WORK_DIR" <<'PY'
import os, random, sys

out = sys.argv[1]

# Semente fixa: o par tem que ser reproduzivel byte a byte, senao regerar as
# fixtures viraria um diff enorme e sem sentido no controle de versao.
rnd = random.Random(20260906)
old = bytearray(rnd.getrandbits(8) for _ in range(256 * 1024))

# Dados pseudo-aleatorios de proposito: um arquivo de zeros comprimiria a
# quase nada e o patch nao exercitaria o descompressor -- que e justamente a
# parte do .so com mais superficie (zstd).
new = bytearray(old)
for off in (0, 40000, 190000):
    # tres edicoes localizadas: cobre "cover" no inicio, no meio e perto do fim
    new[off:off + 512] = bytes((i * 7 + 13) & 0xff for i in range(512))
# ... mais um rabo novo, para o arquivo novo nao ter o mesmo tamanho do velho
new += bytes(rnd.getrandbits(8) for _ in range(8 * 1024))

open(os.path.join(out, "smoke-old.bin"), "wb").write(bytes(old))
open(os.path.join(out, "smoke-new.bin"), "wb").write(bytes(new))
PY

# -s-4m: modo de fluxo com pouca memoria, o mesmo perfil que o aparelho usa.
# -c-zstd-21-24: zstd e o compressor padrao do hdiffz e o que o servidor vai
# gerar; forcar o mesmo aqui e o que faz o teste exercitar o descompressor
# que de fato esta linkado no .so.
"$HDIFFZ" -s-4m -c-zstd-21-24 -f \
  "$WORK_DIR/smoke-old.bin" "$WORK_DIR/smoke-new.bin" "$WORK_DIR/smoke.hdiff" >/dev/null

cp -f "$WORK_DIR/smoke-old.bin" "$ASSETS_DIR/smoke-old.bin"
cp -f "$WORK_DIR/smoke.hdiff"   "$ASSETS_DIR/smoke.hdiff"

echo "make-smoke-fixtures: gravado em $ASSETS_DIR" >&2
echo "  smoke-old.bin  $(stat -c%s "$ASSETS_DIR/smoke-old.bin") bytes  sha256=$(sha256sum "$ASSETS_DIR/smoke-old.bin" | cut -d' ' -f1)" >&2
echo "  smoke.hdiff    $(stat -c%s "$ASSETS_DIR/smoke.hdiff") bytes" >&2
echo >&2
echo "SHA-256 do arquivo NOVO esperado (constante em ApkPatcherSmokeTest):" >&2
sha256sum "$WORK_DIR/smoke-new.bin" | cut -d' ' -f1 >&2
echo "tamanho do arquivo NOVO: $(stat -c%s "$WORK_DIR/smoke-new.bin")" >&2
