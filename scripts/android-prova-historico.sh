#!/bin/bash
# Prova de que o histórico da conversa sobrevive ao "sair e voltar" da sessão.
#
# O que este teste tem que o `android-prova-terminal.sh` não tem: ele SAI da
# tela do terminal e volta. É aí que o defeito vivia — o emulador nasce vazio,
# o `dtach` não guarda tela, e até agora o histórico anterior simplesmente não
# chegava. A prova é ler, depois de voltar, uma linha que só existe no COMEÇO
# da saída, muito acima do que cabe numa tela.
#
# Por que `seq`: cada linha carrega seu próprio número, então a foto do topo do
# scrollback é auto-evidente — "linha 1" só pode ter vindo do histórico.
set -e
A=/opt/android-sdk/platform-tools/adb
S=${SCRATCH:-/tmp/claude-0/-opt-panel/019689ba-fc61-4bcc-aed1-ea24127d6047/scratchpad}
NOME=${1:-hist1}
LINHAS=${2:-3000}

foto() { $A exec-out screencap -p > "$S/$1"; echo "foto $1"; }

$A shell am force-stop tech.northwind.vpsm.app
$A shell am start -n tech.northwind.vpsm.app/com.vpsmanager.app.MainActivity >/dev/null
sleep 12

$A shell input tap 74 214;  sleep 2   # gaveta
$A shell input tap 254 489; sleep 4   # Terminal

# Sessão nova, para nascer viva e vazia.
$A shell input tap 440 726; sleep 2
$A shell input text "$NOME"; sleep 1
$A shell input tap 905 716; sleep 10

# Enche de histórico digitando NA GRADE (nada de EOF pelo socket, que mata o
# shell). O log de pty só grava enquanto alguém está anexado — é exatamente
# esta saída que o primer vai ter de trazer de volta.
$A shell input tap 540 1200; sleep 3
$A shell input text "seq%s1%s$LINHAS"; sleep 1
$A shell input keyevent 66; sleep 12
foto "hist_antes_de_sair.png"

# SAI da sessão (volta para a lista) e VOLTA. Aqui o ViewModel é destruído, a
# engine morre com ele, e o reattach nasce com a grade em branco.
$A shell input keyevent 4; sleep 2   # fecha o teclado
$A shell input keyevent 4; sleep 5   # volta para a lista de sessões
foto "hist_lista.png"

# Reentra na primeira sessão da lista.
$A shell input tap 540 620; sleep 15
foto "hist_ao_voltar.png"

# Sobe o scrollback até o começo. Cada arraste sobe ~uma tela; 60 arrastes
# cobrem folgadamente 3.000 linhas numa grade de ~40 linhas.
for _ in $(seq 1 60); do
  $A shell input swipe 540 700 540 1900 120
done
sleep 3
foto "hist_topo_do_scrollback.png"
echo "pronto — veja $S/hist_topo_do_scrollback.png"
