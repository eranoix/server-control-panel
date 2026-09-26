#!/bin/bash
# Prova de ponta a ponta, do zero: abre o app num estado conhecido, cria uma
# sessao nova, enche de historico digitando NA PROPRIA GRADE (nada de EOF pelo
# socket, que mata o shell), e fotografa com o teclado aberto e fechado.
#
# Comeca por `am start`, e nao por BACK: BACK a partir da Inicio SAI do app, e o
# "teste" vira uma foto da tela inicial do Android.
set -e
A=/opt/android-sdk/platform-tools/adb
S=/tmp/claude-0/-opt-panel/019689ba-fc61-4bcc-aed1-ea24127d6047/scratchpad
NOME=${1:-prova1}

foto() { $A exec-out screencap -p > "$S/$1"; echo "foto $1"; }

$A shell am force-stop tech.northwind.vpsm.app
$A shell am start -n tech.northwind.vpsm.app/com.vpsmanager.app.MainActivity >/dev/null
sleep 12

$A shell input tap 74 214;  sleep 2   # gaveta
$A shell input tap 254 489; sleep 4   # Terminal

# Nova sessao com nome inedito, para nascer viva e vazia.
$A shell input tap 440 726; sleep 2
$A shell input text "$NOME"; sleep 1
$A shell input tap 905 716; sleep 9

# Historico: 400 linhas digitadas na grade.
$A shell input tap 540 1200; sleep 3
$A shell input text "seq%s1%s400"; sleep 1
$A shell input keyevent 66; sleep 5
foto "pf_aberto.png"

# Fecha o teclado (aqui BACK fecha o IME, nao navega) e fotografa de novo.
$A shell input keyevent 4; sleep 3
foto "pf_fechado.png"
echo "pronto"
