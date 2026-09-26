#!/usr/bin/env bash
# check-mobile-openapi-drift.sh — o contrato mobile
# committado (android/data/mobile-api-client/openapi/mobile-v1.yaml) precisa
# continuar sendo exatamente o que `make mobile-openapi-spec` geraria a
# partir do registro huma em internal/mobilebff HOJE.
#
# O QUE ISSO PEGA
# Um handler Go do BFF mudou uma struct tag (ou um campo/rota do registro
# huma) e ninguem rodou `make mobile-openapi-spec` de novo -- o cliente
# Kotlin gerado em :data:mobile-api-client ficaria compilando contra um
# contrato que ja nao bate com o que o servidor realmente responde. Isso e
# exatamente a divergencia de surface que o contrato existe para
# impedir.
#
# COMO
# Regenera o spec (o gerador so sabe escrever no caminho fixo
# android/data/mobile-api-client/openapi/mobile-v1.yaml -- ver
# cmd/mobile-openapi-gen), compara byte a byte contra o arquivo committado, e
# restaura o arquivo original ao final -- sucesso ou falha -- para nunca
# deixar a arvore de trabalho suja.
set -euo pipefail

RAIZ="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$RAIZ"

SPEC_PATH="android/data/mobile-api-client/openapi/mobile-v1.yaml"

if [ ! -f "$SPEC_PATH" ]; then
  echo "check-mobile-openapi-drift: FALHOU -- $SPEC_PATH nao existe neste checkout" >&2
  exit 1
fi

backup="$(mktemp)"
cp "$SPEC_PATH" "$backup"
trap 'cp "$backup" "$SPEC_PATH"; rm -f "$backup"' EXIT

make mobile-openapi-spec

if ! diff -u "$backup" "$SPEC_PATH" > /tmp/mobile-openapi-drift.diff 2>&1; then
  echo "check-mobile-openapi-drift: FALHOU -- $SPEC_PATH esta desatualizado em relacao ao registro huma atual (internal/mobilebff). Rode 'make mobile-openapi-spec' e comite o resultado. Diff:" >&2
  cat /tmp/mobile-openapi-drift.diff >&2
  rm -f /tmp/mobile-openapi-drift.diff
  exit 1
fi
rm -f /tmp/mobile-openapi-drift.diff

echo "check-mobile-openapi-drift: OK -- $SPEC_PATH bate com o que o registro huma atual geraria"
