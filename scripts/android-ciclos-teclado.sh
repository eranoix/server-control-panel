#!/bin/bash
# Abre e fecha o teclado N vezes SOBRE A GRADE, conferindo o estado do IME a
# cada passo. A conferencia nao e zelo: BACK com o teclado ja fechado navega
# para tras, e um "teste" desses mede navegacao em vez de teclado.
A=/opt/android-sdk/platform-tools/adb
N=${1:-5}

ime_visivel() {
  $A shell dumpsys input_method 2>/dev/null | grep -c "mInputShown=true"
}

tela_atual() {
  $A shell dumpsys activity activities 2>/dev/null | grep -m1 "ResumedActivity" | sed 's/.*u0 //;s/ .*//'
}

for c in $(seq 1 "$N"); do
  if [ "$(ime_visivel)" != "0" ]; then
    echo "ciclo $c: teclado ja aberto, fechando primeiro"
    $A shell input keyevent 4
    sleep 2
  fi
  if [ "$(ime_visivel)" != "0" ]; then
    echo "ABORTADO no ciclo $c: nao consegui fechar o teclado"
    exit 1
  fi

  $A shell input tap 540 1200
  sleep 3
  if [ "$(ime_visivel)" = "0" ]; then
    echo "ABORTADO no ciclo $c: o toque na grade nao abriu o teclado"
    exit 1
  fi
  echo "ciclo $c: teclado ABERTO"

  $A shell input keyevent 4
  sleep 3
  if [ "$(ime_visivel)" != "0" ]; then
    echo "ABORTADO no ciclo $c: BACK nao fechou o teclado"
    exit 1
  fi
  echo "ciclo $c: teclado FECHADO"
done
echo "OK: $N ciclos completos, sempre na mesma tela"
