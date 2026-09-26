#!/usr/bin/env bash
# Vendoriza o LADO PATCHER do HDiffPatch (fonte, nunca binario de terceiro)
# nos commits fixados em toolchain.properties.
#
# Por que fonte e nao um .aar/.so pronto: o aparelho vai executar este codigo
# sobre um APK que sera INSTALADO. Um binario baixado de terceiro seria um
# elo da cadeia de suprimento que ninguem neste projeto auditou -- mesma
# regra que :terminal-engine ja aplica (04-PLAN.md, ameaca T-04-SC), so que
# la o artefato commitado e o .a construido aqui e aqui e o .c em si, porque
# o alvo cabe em 2 MB de fonte e o ndk-build o compila no proprio build do
# Gradle.
#
# Copia APENAS os arquivos listados em vendor-files.txt -- fecho transitivo
# real de #include, extraido dos .d do compilador, nao um palpite. Ver o
# cabecalho daquele arquivo e a flag --relist abaixo.
#
# Uso:
#   ./vendor-hdiffpatch.sh            # (re)vendoriza e regrava o manifesto
#   ./vendor-hdiffpatch.sh --relist   # so imprime como regerar vendor-files.txt
#
# Idempotente: rodar de novo com os mesmos commits produz bytes identicos.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

PROPS_FILE="$SCRIPT_DIR/toolchain.properties"
prop() { grep -E "^$1=" "$PROPS_FILE" | tail -1 | cut -d= -f2-; }

BUILD_DIR="$SCRIPT_DIR/.build"
SRC_DIR="$BUILD_DIR/src"
VENDOR_DIR="$SCRIPT_DIR/vendor"

if [ "${1:-}" = "--relist" ]; then
  cat <<'RELIST'
Para regerar vendor-files.txt depois de mexer num interruptor do Android.mk:

  1. ./vendor-hdiffpatch.sh                 # garante .build/src com os commits fixados
  2. cd .build/src/HDiffPatch/builds/android_ndk_jni_mk
     ndk-build NDK_PROJECT_PATH=. APP_BUILD_SCRIPT=Android.mk \
       NDK_APPLICATION_MK=Application.mk APP_ABI="arm64-v8a x86_64" \
       <os mesmos interruptores de src/main/cpp/Android.mk>
  3. Junte todos os obj/local/**/*.o.d, normalize os caminhos relativos e
     escreva a uniao ordenada em vendor-files.txt (prefixo = nome do repo
     irmao: HDiffPatch/, lzma/, zstd/, xxHash/, libmd5/).

O .d e a unica fonte honesta dessa lista: os includes do hpatchz.c dependem
dos proprios -D que os interruptores ligam, entao ler o codigo a olho erra.
RELIST
  exit 0
fi

mkdir -p "$SRC_DIR"

# Cada repo irmao e um checkout raso do commit EXATO. Nunca uma branch: o
# formato do arquivo de diff e a lista de plugins mudam entre versoes, e um
# patch gerado no servidor por uma versao que este .so nao entende falha no
# aparelho, longe de quem poderia depurar.
fetch_pinned() {
  local name="$1" repo="$2" commit="$3"
  local dst="$SRC_DIR/$name"
  if [ -d "$dst/.$VCS_DIR_SUFFIX" ]; then
    local head
    head="$(cd "$dst" && "$VCS" rev-parse HEAD)"
    if [ "$head" = "$commit" ]; then
      echo "vendor-hdiffpatch: $name ja em $commit" >&2
      return 0
    fi
  fi
  rm -rf "$dst"
  mkdir -p "$dst"
  ( cd "$dst"
    "$VCS" init -q
    "$VCS" remote add origin "$repo"
    "$VCS" fetch -q --depth 1 origin "$commit"
    "$VCS" checkout -q FETCH_HEAD )
  echo "vendor-hdiffpatch: $name -> $commit" >&2
}

VCS=git
VCS_DIR_SUFFIX=git

fetch_pinned HDiffPatch "$(prop HDIFFPATCH_REPO)" "$(prop HDIFFPATCH_COMMIT)"
fetch_pinned lzma       "$(prop LZMA_REPO)"       "$(prop LZMA_COMMIT)"
fetch_pinned zstd       "$(prop ZSTD_REPO)"       "$(prop ZSTD_COMMIT)"
fetch_pinned xxHash     "$(prop XXHASH_REPO)"     "$(prop XXHASH_COMMIT)"
fetch_pinned libmd5     "$(prop LIBMD5_REPO)"     "$(prop LIBMD5_COMMIT)"

echo "vendor-hdiffpatch: copiando $(grep -cvE '^\s*(#|$)' vendor-files.txt) arquivos" >&2
rm -rf "$VENDOR_DIR/HDiffPatch" "$VENDOR_DIR/lzma" "$VENDOR_DIR/zstd" \
       "$VENDOR_DIR/xxHash" "$VENDOR_DIR/libmd5"
mkdir -p "$VENDOR_DIR"

while IFS= read -r rel; do
  case "$rel" in ''|\#*) continue ;; esac
  mkdir -p "$VENDOR_DIR/$(dirname "$rel")"
  cp -p "$SRC_DIR/$rel" "$VENDOR_DIR/$rel"
done < vendor-files.txt

# O binding Java oficial. Fica FORA de vendor-files.txt de proposito: aquela
# lista e o fecho de #include do compilador C, e este arquivo nao e C. Vem
# literal, sem uma linha alterada, porque o simbolo JNI compilado em
# hpatch_jni.c e literalmente Java_com_github_sisong_HPatch_patch -- mudar o
# pacote da classe quebraria o vinculo em runtime, nao em tempo de compilacao.
# build.gradle.kts registra o diretorio abaixo como srcDir de java.
JAVA_REL="HDiffPatch/builds/android_ndk_jni_mk/java"
mkdir -p "$VENDOR_DIR/$JAVA_REL"
cp -pR "$SRC_DIR/$JAVA_REL/." "$VENDOR_DIR/$JAVA_REL/"

# Licencas: cada repo irmao entra com a sua propria, ao lado do codigo. O app
# tem tela de licencas -- o que e distribuido no APK precisa estar listado la,
# e nao da para listar o que nao esta escrito aqui.
cp -p "$SRC_DIR/HDiffPatch/LICENSE" "$VENDOR_DIR/HDiffPatch/LICENSE"
cp -p "$SRC_DIR/zstd/LICENSE"       "$VENDOR_DIR/zstd/LICENSE"
cp -p "$SRC_DIR/xxHash/LICENSE"     "$VENDOR_DIR/xxHash/LICENSE"
cp -p "$SRC_DIR/lzma/DOC/lzma-sdk.txt" "$VENDOR_DIR/lzma/LICENSE-lzma-sdk.txt"
# libmd5 nao tem arquivo de licenca separado: o texto (zlib-like, Aladdin
# Enterprises / L. Peter Deutsch) vive no cabecalho do proprio md5.h, que ja
# esta vendorizado.

python3 - "$VENDOR_DIR" "$PROPS_FILE" <<'PY'
import hashlib, json, os, subprocess, sys

vendor, props_file = sys.argv[1], sys.argv[2]
props = {}
for line in open(props_file):
    line = line.strip()
    if line and not line.startswith("#") and "=" in line:
        k, v = line.split("=", 1)
        props[k] = v

files = {}
for root, _dirs, names in os.walk(vendor):
    for n in sorted(names):
        p = os.path.join(root, n)
        rel = os.path.relpath(p, vendor)
        if rel == "vendor-manifest.json":
            continue
        with open(p, "rb") as fh:
            data = fh.read()
        files[rel] = {"sha256": hashlib.sha256(data).hexdigest(), "size_bytes": len(data)}

manifest = {
    "commits": {
        "HDiffPatch": props["HDIFFPATCH_COMMIT"],
        "lzma": props["LZMA_COMMIT"],
        "zstd": props["ZSTD_COMMIT"],
        "xxHash": props["XXHASH_COMMIT"],
        "libmd5": props["LIBMD5_COMMIT"],
    },
    "repos": {
        "HDiffPatch": props["HDIFFPATCH_REPO"],
        "lzma": props["LZMA_REPO"],
        "zstd": props["ZSTD_REPO"],
        "xxHash": props["XXHASH_REPO"],
        "libmd5": props["LIBMD5_REPO"],
    },
    "hdiffpatch_tag": props.get("HDIFFPATCH_TAG", ""),
    "ndk_version": props["NDK_VERSION"],
    "file_count": len(files),
    "total_bytes": sum(f["size_bytes"] for f in files.values()),
    "files": dict(sorted(files.items())),
}
out = os.path.join(vendor, "vendor-manifest.json")
with open(out, "w") as fh:
    json.dump(manifest, fh, indent=2, sort_keys=False)
    fh.write("\n")
print("vendor-hdiffpatch: %d arquivos, %d bytes" % (len(files), manifest["total_bytes"]), file=sys.stderr)
PY

echo "vendor-hdiffpatch: pronto" >&2
