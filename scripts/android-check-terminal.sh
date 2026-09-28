#!/bin/bash
set -e
A=/opt/android-sdk/platform-tools/adb
S=${SCRATCH:-/tmp}
NAME=${1:-check1}

shot() { $A exec-out screencap -p > "$S/$1"; echo "screenshot $1"; }

$A shell am force-stop tech.northwind.servercontrolpanel
$A shell am start -n tech.northwind.servercontrolpanel/dev.servercontrolpanel.app.MainActivity >/dev/null
sleep 12

$A shell input tap 74 214;  sleep 2
$A shell input tap 254 489; sleep 4

$A shell input tap 440 726; sleep 2
$A shell input text "$NAME"; sleep 1
$A shell input tap 905 716; sleep 9

$A shell input tap 540 1200; sleep 3
$A shell input text "seq%s1%s400"; sleep 1
$A shell input keyevent 66; sleep 5
shot "pf_open.png"

$A shell input keyevent 4; sleep 3
shot "pf_closed.png"
echo "done"
