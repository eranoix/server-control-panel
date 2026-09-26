#!/bin/bash
# Prova de ponta a ponta do ANEXO pelo dock do terminal: escolher um arquivo,
# subir em pedaços e ver o caminho aparecer DIGITADO na sessão.
#
# Por que este script existe: o anexo é a única função do app entregue sem
# nenhuma prova contra o servidor de verdade — os testes cobrem as peças
# (pump, worker, citação do nome), nenhum cobre a COSTURA, que é exatamente
# onde os defeitos deste projeto moram.
#
# PRÉ-REQUISITO QUE NENHUM SCRIPT RESOLVE: o app precisa estar LOGADO. Nenhuma
# sessão de agente tem senha de conta do painel, e é por isso que esta prova
# ficou aberta. Faça o login uma vez no emulador (ou no aparelho) e rode isto
# em seguida — a sessão sobrevive a `force-stop`.
#
# Uso:  scripts/android-prova-anexo.sh [arquivo-de-origem]
set -euo pipefail

A=${ADB:-/opt/android-sdk/platform-tools/adb}
PKG=tech.northwind.vpsm.app
S=${SCRATCH:-/tmp}
ORIGEM=${1:-}
DATA_DIR=${VPSM_DATA_DIR:-/opt/panel/data}
INBOX="$DATA_DIR/mobile-inbox"
STAGING="$DATA_DIR/.mobile-upload-staging"

# Nome com espaço de propósito: ele vira parte de um caminho colado num shell,
# e a citação é o que separa "um argumento" de "dois argumentos e um erro".
if [ -z "$ORIGEM" ]; then
  ORIGEM="$S/anexo de prova.txt"
  head -c 300000 /dev/urandom | base64 > "$ORIGEM"
fi
NOME_REMOTO="$(basename "$ORIGEM")"
SHA_ORIGEM="$(sha256sum "$ORIGEM" | cut -d' ' -f1)"

echo "== origem: $ORIGEM ($(stat -c%s "$ORIGEM") bytes, sha256 ${SHA_ORIGEM:0:12}…)"

echo "== 1. semeando o arquivo onde o seletor do sistema enxerga =="
$A push "$ORIGEM" "/sdcard/Download/$NOME_REMOTO" >/dev/null

echo "== 2. abrindo o terminal num estado conhecido =="
$A logcat -c
$A shell am force-stop $PKG || true
$A shell am start -n $PKG/com.vpsmanager.app.MainActivity >/dev/null
sleep 12
$A shell input tap 74 214;  sleep 2   # gaveta
$A shell input tap 254 489; sleep 5   # Terminal

echo
echo "== 3. AGORA É COM VOCÊ (o seletor de arquivos é do SISTEMA, não do app) =="
echo "   toque no clipe na barra de teclas → escolha '$NOME_REMOTO' em Downloads"
echo "   → espere a faixa de progresso → toque para inserir."
echo "   Enquanto isso, isto aqui vigia os dois lados."
echo
read -r -p "   pressione ENTER quando tiver inserido (ou Ctrl-C para abortar) " _

echo "== 4. o arquivo chegou INTEIRO? =="
if [ ! -f "$INBOX/$NOME_REMOTO" ]; then
  echo "FALHOU: $INBOX/$NOME_REMOTO não existe" >&2
  ls -la "$INBOX" || true
  exit 1
fi
SHA_DESTINO="$(sha256sum "$INBOX/$NOME_REMOTO" | cut -d' ' -f1)"
if [ "$SHA_ORIGEM" != "$SHA_DESTINO" ]; then
  echo "FALHOU: sha256 diferente — origem $SHA_ORIGEM, destino $SHA_DESTINO" >&2
  exit 1
fi
echo "OK: bytes idênticos"

echo "== 5. a área de montagem ficou limpa? =="
# Um staging com sobra significa CompleteUpload movendo por cópia em vez de
# rename, ou sessão abandonada — os dois viram lixo que cresce sozinho.
if [ -d "$STAGING" ] && [ -n "$(ls -A "$STAGING" 2>/dev/null)" ]; then
  echo "ATENÇÃO: sobrou coisa em $STAGING:" >&2
  ls -la "$STAGING" >&2
else
  echo "OK: staging vazio"
fi

echo "== 6. o caminho foi DIGITADO na sessão? =="
echo "   (confira na foto; o caminho tem de estar citado como UM argumento)"
$A exec-out screencap -p > "$S/anexo-inserido.png"
echo "   foto: $S/anexo-inserido.png"

echo "== 7. o worker reclamou de alguma coisa? =="
$A logcat -d | grep -Ei "WM-|AnexoUpload|Could not create Worker|TransferRepository" | tail -15 || echo "   (nada)"

echo
echo "Os quatro casos que JÁ falharam antes e merecem uma rodada cada:"
echo "  a) socket morto antes de inserir  → tem de avisar e MANTER a linha"
echo "  b) nome com espaço               → tem de chegar citado, um argumento só"
echo "  c) modo avião no meio do upload  → tem de retomar no byte confirmado"
echo "  d) arquivo vazio                 → 'O arquivo está vazio', nunca um 413"
