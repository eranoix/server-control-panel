#!/bin/bash
# End-to-end check from scratch: open the app in a known state, create a new
# session, fill it with history by typing ON THE GRID (an EOF over the socket
# kills the shell), and take screenshots with the keyboard open and closed.
#
# Starts with `am start`, not BACK: BACK from the home screen LEAVES the app.
set -e
A=/opt/android-sdk/platform-tools/adb
S=${SCRATCH:-/tmp}
NAME=${1:-check1}

shot() { $A exec-out screencap -p > "$S/$1"; echo "screenshot $1"; }

$A shell am force-stop tech.northwind.servercontrolpanel
$A shell am start -n tech.northwind.servercontrolpanel/dev.servercontrolpanel.app.MainActivity >/dev/null
sleep 12

$A shell input tap 74 214;  sleep 2   # drawer
$A shell input tap 254 489; sleep 4   # Terminal

# New session with an unused name, so it starts alive and empty.
$A shell input tap 440 726; sleep 2
$A shell input text "$NAME"; sleep 1
$A shell input tap 905 716; sleep 9

# History: 400 lines typed on the grid.
$A shell input tap 540 1200; sleep 3
$A shell input text "seq%s1%s400"; sleep 1
$A shell input keyevent 66; sleep 5
shot "pf_open.png"

# Close the keyboard (here BACK closes the IME, it does not navigate).
$A shell input keyevent 4; sleep 3
shot "pf_closed.png"
echo "done"
