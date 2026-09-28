#!/bin/bash
set -e
A=/opt/android-sdk/platform-tools/adb
S=${SCRATCH:-/tmp}
NAME=${1:-hist1}
LINES=${2:-3000}

shot() { $A exec-out screencap -p > "$S/$1"; echo "screenshot $1"; }

$A shell am force-stop tech.northwind.servercontrolpanel
$A shell am start -n tech.northwind.servercontrolpanel/dev.servercontrolpanel.app.MainActivity >/dev/null
sleep 12

$A shell input tap 74 214;  sleep 2
$A shell input tap 254 489; sleep 4

$A shell input tap 440 726; sleep 2
$A shell input text "$NAME"; sleep 1
$A shell input tap 905 716; sleep 10

$A shell input tap 540 1200; sleep 3
$A shell input text "seq%s1%s$LINES"; sleep 1
$A shell input keyevent 66; sleep 12
shot "hist_before_leaving.png"

$A shell input keyevent 4; sleep 2
$A shell input keyevent 4; sleep 5
shot "hist_list.png"

$A shell input tap 540 620; sleep 15
shot "hist_on_return.png"

for _ in $(seq 1 60); do
  $A shell input swipe 540 700 540 1900 120
done
sleep 3
shot "hist_scrollback_top.png"
echo "done: see $S/hist_scrollback_top.png"
