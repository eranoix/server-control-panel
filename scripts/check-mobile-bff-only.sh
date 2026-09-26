#!/usr/bin/env bash
# check-mobile-bff-only.sh — rede de CI independente de Gradle/JDK.
#
# O QUE ESTE GATE PROTEGE
# O unico surface HTTP legal do app e /api/mobile/v1/* via o cliente gerado
# em :data:mobile-api-client. Este script reforca as MESMAS duas classes de
# violacao que o gate lexical do Gradle (BffOnlyNetworkPlugin, plano 01-05),
# so que so com grep/awk -- roda no runner self-hosted mesmo antes de (ou sem)
# um build Gradle completo:
#
#   1. import direto de okhttp3./retrofit2. fora do BFF
#   2. literal de string citando uma rota /api/* que nao comeca com o prefixo
#      permitido /api/mobile/v1 (e nao e um dos caminhos de WebSocket ja
#      aprovados como contrato separado)
#
# O QUE ESTE GATE NAO PROTEGE (de proposito, nao por descuido)
# Rota construida por concatenacao/interpolacao, ou uma referencia totalmente
# qualificada sem import (`okhttp3.OkHttpClient()`) -- nenhum mecanismo
# baseado em texto enxerga essas duas formas. Essa classe de bypass e fechada
# pela regra detekt com resolucao de tipo (BffOnlyNetworkClientRule, em
# android/build-logic/lint-rules), que reasona sobre o TIPO resolvido pelo
# compilador Kotlin, nunca sobre o texto-fonte. Ver <threat_model> do plano
# 01-05 para o risco residual aceito (cliente HTTP cru sobre
# java.net.HttpURLConnection/Socket, que nenhuma das tres camadas enxerga).
#
# ESCOPO
# Modulos de app Android (android/**/src/main|test/kotlin/**/*.kt) --
# EXCLUI android/data/** (o par autorizado :data + :data:mobile-api-client,
# unico lugar onde okhttp3/retrofit2 e uma rota /api/* fora do prefixo
# mobile/v1 sao esperados) e EXCLUI android/build-logic/** (implementa os
# proprios gates, nao e codigo de app -- suas doc-comments citam
# "import okhttp3." e "/api/..." como PROSA explicando a regra, nao como
# violacao real; ver a lacuna abaixo).
#
# LACUNA DE FALSO-POSITIVO JA CONHECIDA NESTE PROJETO (por isso o cuidado
# extra abaixo, em vez de um grep ingenuo)
# Uma fase anterior teve um gate que so pulava linha comecando com `//` e
# disparava falso-positivo em prosa de KDoc (`/** ... */`) que so EXPLICA o
# token proibido, sem de fato violar a regra. Este script:
#   (a) apaga o conteudo de blocos /* ... */ (inclusive KDoc) ANTES de casar
#       qualquer padrao -- nao so a convencao `//` de linha inteira;
#   (b) so entao aplica a convencao do repo: linha (apos strip de bloco e
#       trim) comecando com `//` e pulada inteira.
# Simplificacao aceita: blocos de comentario aninhados (Kotlin permite; C/Java
# nao) nao sao tratados de forma recursiva aqui -- o tokenizer completo que
# trata isso corretamente ja existe no gate autoritativo (BffOnlyNetworkPlugin
# e BffOnlyNetworkClientRule); este script e uma rede adicional, nao a fonte
# da verdade.
set -euo pipefail

RAIZ="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$RAIZ"

ALLOWED_API_PREFIX="/api/mobile/v1"
WS_EXEMPT_1="/ws/shell"
WS_EXEMPT_2="/ws/videocall"

# Remove blocos /* ... */ (nao-aninhado) preservando numero de linha, depois
# aplica a convencao `//` de linha inteira -- ver cabecalho acima.
strip_comments() {
  awk '
    BEGIN { in_block = 0 }
    {
      line = $0
      out = ""
      while (length(line) > 0) {
        if (in_block) {
          pos = index(line, "*/")
          if (pos == 0) { line = ""; break }
          line = substr(line, pos + 2)
          in_block = 0
          continue
        }
        pos = index(line, "/*")
        if (pos == 0) { out = out line; line = ""; break }
        out = out substr(line, 1, pos - 1)
        line = substr(line, pos + 2)
        in_block = 1
      }
      trimmed = out
      sub(/^[ \t]+/, "", trimmed)
      if (index(trimmed, "//") == 1) { out = "" }
      print out
    }
  '
}

violations_file="$(mktemp)"
trap 'rm -f "$violations_file"' EXIT

while IFS= read -r -d '' kt_file; do
  code_only="$(strip_comments < "$kt_file")"

  line_no=0
  while IFS= read -r code_line; do
    line_no=$((line_no + 1))
    trimmed="$(printf '%s' "$code_line" | sed -e 's/^[[:space:]]*//')"

    case "$trimmed" in
      "import okhttp3."*|"import retrofit2."*)
        echo "$kt_file:$line_no: import proibido fora do BFF -- $trimmed" >> "$violations_file"
        ;;
    esac

    while [[ "$code_line" =~ (/api/[A-Za-z0-9._/-]*) ]]; do
      match="${BASH_REMATCH[1]}"
      rest="${code_line#*"$match"}"
      code_line="$rest"
      if [[ "$match" == "$ALLOWED_API_PREFIX"* ]]; then
        continue
      fi
      if [[ "$match" == *"$WS_EXEMPT_1"* || "$match" == *"$WS_EXEMPT_2"* ]]; then
        continue
      fi
      echo "$kt_file:$line_no: rota \"$match\" fora do BFF mobile/v1" >> "$violations_file"
    done
  done <<< "$code_only"
done < <(find android \
  -path "android/data" -prune -o \
  -path "android/build-logic" -prune -o \
  -path "*/build/*" -prune -o \
  -type f -name "*.kt" -print0)

if [ -s "$violations_file" ]; then
  echo "check-mobile-bff-only: FALHOU -- referencia a rota /api/* fora do BFF mobile/v1 ou import direto de okhttp3/retrofit2:" >&2
  cat "$violations_file" >&2
  exit 1
fi

echo "check-mobile-bff-only: OK -- nenhum modulo de app referencia okhttp3/retrofit2 ou uma rota /api/* fora do prefixo mobile/v1"
