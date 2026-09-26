#!/usr/bin/env bash
# setup-hdiffpatch.sh — provisiona hdiffz/hpatchz (HDiffPatch, licença MIT)
# nesta VPS. Idempotente: seguro rodar de novo a qualquer momento.
#
# hdiffz é a ferramenta que scripts/android-patches.sh usa para produzir os
# patches binários entre APKs assinados. hpatchz é o par que aplica — no
# aparelho é o libhpatchz.so do SDK Android oficial, mas ter o binário aqui
# permite VERIFICAR, no próprio servidor, que o patch gerado reconstrói bytes
# idênticos ao APK assinado antes de publicá-lo.
#
# POR QUE BINÁRIO OFICIAL E NÃO BUILD DA FONTE
# O build da fonte exige os submódulos (lzma, zstd, libmd5) que o tarball do
# GitHub não traz; um clone recursivo em cada máquina é mais frágil, não
# menos, que um artefato oficial verificado por SHA-256 fixado aqui. O
# binário é estático (sem dependência de libc do host) e a checagem de hash
# abaixo é o que garante que o que foi baixado é o que foi auditado.
#
# Verificação de integridade: SHA-256 conferido contra o valor fixado abaixo.
# Ao subir de versão, atualize VERSAO e SHA256_ESPERADO juntos — e regere os
# patches, porque o formato de saída pode mudar entre versões maiores.
set -euo pipefail

VERSAO="v5.1.3"
ARQUIVO="hdiffpatch_${VERSAO}_bin_linux64.zip"
URL="https://github.com/sisong/HDiffPatch/releases/download/${VERSAO}/${ARQUIVO}"
SHA256_ESPERADO="628963bf2ee9108a97260fa5eef44acd9ec94369b76090a957c9182b3abbb558"
DESTINO="${DESTINO:-/usr/local/bin}"

fail() { echo "ERRO: $*" >&2; exit 1; }

# hdiffz/hpatchz sem argumentos imprimem o banner de uso e saem com status
# != 0; sob `set -e -o pipefail` isso abortaria o script. Daí o `|| true`.
versao_de() { { "$1" 2>&1 || true; } | head -1; }

if command -v hdiffz >/dev/null 2>&1 && command -v hpatchz >/dev/null 2>&1; then
  instalada="$(versao_de hdiffz)"
  case "$instalada" in
    *"${VERSAO#v}"*)
      echo "already provisioned: $instalada"
      exit 0
      ;;
  esac
  echo "AVISO: hdiffz presente mas em outra versão ($instalada); reinstalando ${VERSAO}"
fi

command -v curl >/dev/null 2>&1 || fail "curl não encontrado"
command -v unzip >/dev/null 2>&1 || fail "unzip não encontrado"
command -v sha256sum >/dev/null 2>&1 || fail "sha256sum não encontrado"

TMP="$(mktemp -d "${TMPDIR:-/tmp}/vpsm-hdiffpatch.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

echo "==> baixando ${ARQUIVO}"
curl -fsSL -o "$TMP/$ARQUIVO" "$URL" || fail "download falhou: $URL"

obtido="$(sha256sum "$TMP/$ARQUIVO" | cut -d' ' -f1)"
[ "$obtido" = "$SHA256_ESPERADO" ] \
  || fail "SHA-256 do download NÃO confere — esperado $SHA256_ESPERADO, obtido $obtido. Nada foi instalado."
echo "OK: SHA-256 confere"

unzip -q -o "$TMP/$ARQUIVO" -d "$TMP/extraido"
for bin in hdiffz hpatchz; do
  origem="$TMP/extraido/linux64/$bin"
  [ -f "$origem" ] || fail "$bin não veio no pacote"
  install -m 0755 "$origem" "$DESTINO/$bin"
done

echo "OK: $(versao_de "$DESTINO/hdiffz") e $(versao_de "$DESTINO/hpatchz") instalados em $DESTINO"
