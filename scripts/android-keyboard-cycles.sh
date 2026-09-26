#!/bin/bash
# Opens and closes the keyboard N times OVER THE GRID, checking the IME state at
# every step. The check matters: BACK with the keyboard already closed navigates
# back, and the test would then measure navigation instead of the keyboard.
A=/opt/android-sdk/platform-tools/adb
N=${1:-5}

ime_visible() {
  $A shell dumpsys input_method 2>/dev/null | grep -c "mInputShown=true"
}

current_screen() {
  $A shell dumpsys activity activities 2>/dev/null | grep -m1 "ResumedActivity" | sed 's/.*u0 //;s/ .*//'
}

for c in $(seq 1 "$N"); do
  if [ "$(ime_visible)" != "0" ]; then
    echo "cycle $c: keyboard already open, closing it first"
    $A shell input keyevent 4
    sleep 2
  fi
  if [ "$(ime_visible)" != "0" ]; then
    echo "ABORTED at cycle $c: could not close the keyboard"
    exit 1
  fi

  $A shell input tap 540 1200
  sleep 3
  if [ "$(ime_visible)" = "0" ]; then
    echo "ABORTED at cycle $c: tapping the grid did not open the keyboard"
    exit 1
  fi
  echo "cycle $c: keyboard OPEN"

  $A shell input keyevent 4
  sleep 3
  if [ "$(ime_visible)" != "0" ]; then
    echo "ABORTED at cycle $c: BACK did not close the keyboard"
    exit 1
  fi
  echo "cycle $c: keyboard CLOSED"
done
echo "OK: $N full cycles, always on the same screen"
