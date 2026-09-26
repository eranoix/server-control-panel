#!/usr/bin/env bash
# android-publish.sh <versionCode> — publica uma release assinada no
# repositório F-Droid servido por este host (data/fdroid/repo/).
#
# CUSTÓDIA DE CHAVES (docs/android-signing-keystore.md,
# docs/android-fdroid-repo.md): tanto a chave mestra de assinatura do APK
# quanto a `repokey` de assinatura do índice F-Droid são geradas e usadas
# EXCLUSIVAMENTE na máquina offline do operador. Este script roda nesta VPS
# e NUNCA lê, solicita ou manipula nenhuma das duas chaves privadas — ele só:
#
#   1. verifica o fingerprint SHA-256 PÚBLICO do certificado da APK já
#      assinada contra o valor registrado em docs/android-signing-keystore.md
#      (defesa em profundidade: mesmo que o operador já tenha conferido isso
#      manualmente na Task 2, este gate garante que nenhuma APK com
#      assinatura errada ou corrompida chega ao repositório servido);
#   2. aplica, de forma atômica, um pacote de repositório F-Droid que chega
#      JÁ TOTALMENTE ASSINADO — porque `fdroid update` (a operação que
#      precisa da repokey) roda inteiramente na máquina do operador, nunca
#      aqui (docs/android-fdroid-repo.md, seção 6).
#
# Por isso este script NUNCA exporta nada de data/secrets.vault e nunca
# invoca `fdroid update` diretamente — ao contrário do desenho original do
# plano 12-03 (que assumia a repokey em data/secrets.vault), essa premissa
# foi corrigida pela decisão já travada em 12-01/docs/android-fdroid-repo.md
# §1-2: nenhuma chave de assinatura de distribuição pública fica acessível a
# partir do host que também serve o tráfego público. Ver
# docs/android-release-pipeline.md para o fluxo completo do operador.
#
# ENTRADAS (convenção fixa de staging, mesmo padrão das Tasks 1/2):
#   data/android-release-staging/<versionCode>/app-release-signed.apk
#     — produzida pela assinatura offline do operador (Task 2).
#   data/android-release-staging/<versionCode>/fdroid-repo/
#     — a própria data/fdroid/repo/ do operador, DEPOIS de ele rodar
#       `fdroid update` na máquina dele (com a repokey offline) contra o
#       espelho canônico do repositório. Já contém a APK copiada e o índice
#       regenerado e assinado (index-v2.json, index-v1.jar, entry.json,
#       entry.jar, ícones). Este script nunca gera nada disso — só valida e
#       publica.
#
# Variáveis de ambiente (override para teste; produção usa os defaults):
#   STAGING_DIR, FDROID_REPO_DIR, KEYSTORE_DOC, APKSIGNER
set -euo pipefail

VERSION_CODE="${1:?uso: scripts/android-publish.sh <versionCode>}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STAGING_DIR="${STAGING_DIR:-$ROOT_DIR/data/android-release-staging}"
FDROID_REPO_DIR="${FDROID_REPO_DIR:-$ROOT_DIR/data/fdroid/repo}"
KEYSTORE_DOC="${KEYSTORE_DOC:-$ROOT_DIR/docs/android-signing-keystore.md}"
APKSIGNER="${APKSIGNER:-apksigner}"

RELEASE_DIR="$STAGING_DIR/$VERSION_CODE"
SIGNED_APK="$RELEASE_DIR/app-release-signed.apk"
REPO_BUNDLE="$RELEASE_DIR/fdroid-repo"

fail() {
  echo "ERRO: $*" >&2
  exit 1
}

# Normaliza um fingerprint SHA-256 para comparação: remove os dois-pontos
# (formato do keytool, ex. "66:2D:33...") e caixa-baixa tudo — o apksigner
# imprime sem dois-pontos e em minúsculas ("662d3390...").
normalize_fp() {
  tr -d ':[:space:]' <<<"$1" | tr '[:upper:]' '[:lower:]'
}

[ -f "$SIGNED_APK" ] || fail "APK assinada não encontrada em $SIGNED_APK — rode a Task 2 (assinatura offline) primeiro"

EXPECTED_LINE=$(grep -m1 '^SHA-256:' "$KEYSTORE_DOC" || true)
[ -n "$EXPECTED_LINE" ] || fail "nenhuma linha 'SHA-256:' encontrada em $KEYSTORE_DOC"
EXPECTED_RAW=$(sed -E 's/^SHA-256:[[:space:]]*//' <<<"$EXPECTED_LINE")
case "$EXPECTED_RAW" in
  *PENDENTE*) fail "fingerprint em $KEYSTORE_DOC ainda está como PENDENTE — o keystore de release ainda não foi gerado (Task 2)" ;;
esac
EXPECTED_FP="$(normalize_fp "$EXPECTED_RAW")"

ACTUAL_LINE=$("$APKSIGNER" verify --print-certs "$SIGNED_APK" 2>&1 | grep -m1 'certificate SHA-256 digest:') \
  || fail "'apksigner verify' falhou para $SIGNED_APK (assinatura inválida, corrompida, ou apksigner não encontrado)"
ACTUAL_RAW=$(sed -E 's/.*certificate SHA-256 digest:[[:space:]]*//' <<<"$ACTUAL_LINE")
ACTUAL_FP="$(normalize_fp "$ACTUAL_RAW")"

[ -n "$ACTUAL_FP" ] || fail "não foi possível extrair o fingerprint SHA-256 da saída do apksigner"

if [ "$ACTUAL_FP" != "$EXPECTED_FP" ]; then
  fail "fingerprint NÃO bate — esperado $EXPECTED_FP, obtido $ACTUAL_FP. APK recusada, nada foi publicado."
fi

echo "OK: fingerprint da APK confere com $KEYSTORE_DOC (versionCode=$VERSION_CODE)"

[ -d "$REPO_BUNDLE" ] || fail "pacote de repositório assinado não encontrado em $REPO_BUNDLE — rode 'fdroid update' offline na máquina do operador (docs/android-fdroid-repo.md §6) e envie o resultado para cá antes de publicar"

INDEX_JSON="$REPO_BUNDLE/index-v2.json"
[ -f "$INDEX_JSON" ] || fail "$REPO_BUNDLE não contém index-v2.json — pacote de repositório incompleto"

APK_BASENAME="$(basename "$SIGNED_APK")"
BUNDLED_APK="$REPO_BUNDLE/$APK_BASENAME"
[ -f "$BUNDLED_APK" ] || fail "$REPO_BUNDLE não contém $APK_BASENAME — o índice foi gerado sem esta APK"

if ! grep -q "$APK_BASENAME" "$INDEX_JSON"; then
  fail "index-v2.json em $REPO_BUNDLE não referencia $APK_BASENAME — índice desatualizado ou gerado antes desta APK (Pitfall 14)"
fi
echo "OK: index-v2.json referencia $APK_BASENAME"

mkdir -p "$FDROID_REPO_DIR"
# --delete-after: o pacote do operador é o novo estado canônico completo do
# diretório servido (ele já contém todo o histórico de APKs + índice
# assinado) — não é um delta parcial, então substituir por completo é
# correto aqui. Nunca redireciona (Pitfall 14): o rsync copia bytes reais
# para o mesmo caminho que o índice referencia.
rsync -a --delete-after "$REPO_BUNDLE"/ "$FDROID_REPO_DIR"/

echo "OK: repositório F-Droid publicado em $FDROID_REPO_DIR (versionCode=$VERSION_CODE)"

# ── atualização incremental (patches HDiffPatch) ──────────────────────────
# Roda DEPOIS do gate de fingerprint e DEPOIS da publicação, de propósito: o
# patch tem que ser gerado dos bytes do APK **já assinado**, e este é o único
# momento em que este host os tem em mãos (a assinatura é offline por
# desenho). Gerar do artefato não-assinado produziria um patch que reconstrói
# um arquivo que o Android recusa instalar.
#
# Falha aqui NÃO desfaz a publicação: o repositório F-Droid acima já está no
# ar e é o canal que sempre funciona. Sem o catálogo, o app só perde o
# caminho incremental (GET /app/update responde "canal ainda não publicado")
# — degradação, não quebra. Por isso o aviso em vez de exit 1.
if [ "${SKIP_PATCHES:-0}" != "1" ]; then
  if "$ROOT_DIR/scripts/android-patches.sh"; then
    :
  else
    echo "AVISO: geração de patches incrementais falhou — a release F-Droid está publicada e íntegra, mas o canal de atualização incremental ficou desatualizado. Rode scripts/android-patches.sh manualmente." >&2
  fi
fi
