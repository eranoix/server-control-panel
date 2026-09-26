#!/usr/bin/env bash
# android-keystore-drill.sh — valida o procedimento de geração + backup
# cifrado + restauração + verificação de fingerprint descrito em
# docs/android-signing-keystore.md, sem tocar em nenhum material de chave
# real.
#
# O que faz: roda inteiramente dentro de um diretório temporário descartável
# (mktemp -d, apagado por um trap no EXIT), gera um keystore JATOP com os
# MESMOS parâmetros de segurança da seção 3 do runbook (RSA 4096, validade
# 10000 dias, PKCS12, alias vpsmanager), cifra o resultado com uma senha
# aleatória (openssl aes-256-cbc), restaura essa cópia cifrada em outro
# diretório, e compara programaticamente o fingerprint SHA-256 do original
# com o do restaurado. Sai com código != 0 se o fingerprint divergir, se
# faltar alguma ferramenta, ou se algo no meio do caminho falhar.
#
# Difere do comando interativo da seção 3 apenas por acrescentar
# -dname/-storepass:env/-keypass:env para rodar sem TTY — os parâmetros que
# importam para a chave (alias, algoritmo, tamanho, validade, formato) são
# idênticos.
#
# Nenhuma senha ou material de chave é impresso em nenhum momento; o único
# valor que aparece na saída é o fingerprint SHA-256, que não é secreto.
#
# Uso: scripts/android-keystore-drill.sh
# Requer: keytool, openssl (ambos padrão em qualquer JDK 17+ / Linux).

set -euo pipefail

ALIAS="vpsmanager"

for bin in keytool openssl; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "ERRO: $bin não encontrado no PATH — não dá para rodar o drill" >&2
    exit 1
  fi
done

WORKDIR="$(mktemp -d -t android-keystore-drill.XXXXXX)"
trap 'rm -rf "$WORKDIR"' EXIT

echo "== drill de keystore descartável — diretório temporário: $WORKDIR =="

# Senhas de descarte, geradas aleatoriamente e nunca literais em nenhum
# argumento de shell (ficariam no histórico/ps aux) — passadas ao keytool
# via variável de ambiente (-storepass:env / -keypass:env).
STORE_PASS="$(openssl rand -base64 24)"
KEY_PASS="$(openssl rand -base64 24)"
ENC_PASS="$(openssl rand -base64 24)"
export STORE_PASS KEY_PASS ENC_PASS

KEYSTORE="$WORKDIR/drill-keystore.jks"
BACKUP_ENC="$WORKDIR/drill-keystore.jks.enc"
RESTORE_DIR="$WORKDIR/restore"
mkdir -p "$RESTORE_DIR"
RESTORED_KEYSTORE="$RESTORE_DIR/drill-keystore.jks"

echo "== gerando keystore descartável (mesmos parâmetros da seção 3 do runbook) =="
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
echo "fingerprint original  : $FP_ORIGINAL"

echo "== cifrando backup (openssl aes-256-cbc, pbkdf2, senha de descarte) =="
openssl enc -aes-256-cbc -pbkdf2 -salt -pass env:ENC_PASS \
  -in "$KEYSTORE" -out "$BACKUP_ENC"

echo "== restaurando o backup cifrado em outro diretório =="
openssl enc -d -aes-256-cbc -pbkdf2 -pass env:ENC_PASS \
  -in "$BACKUP_ENC" -out "$RESTORED_KEYSTORE"

FP_RESTORED="$(extract_sha256 "$RESTORED_KEYSTORE")"
echo "fingerprint restaurado : $FP_RESTORED"

if [ -z "$FP_ORIGINAL" ] || [ -z "$FP_RESTORED" ]; then
  echo "ERRO: não foi possível extrair um fingerprint SHA-256 válido" >&2
  exit 1
fi

if [ "$FP_ORIGINAL" != "$FP_RESTORED" ]; then
  echo "ERRO: fingerprint divergente entre original e restaurado — drill FALHOU" >&2
  exit 1
fi

echo "OK: geração + backup cifrado + restauração + verificação de fingerprint bateram."
echo "(artefato inteiramente descartável — diretório temporário será apagado ao sair)"
