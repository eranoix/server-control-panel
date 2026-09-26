#!/bin/bash
# Checks that the session history survives leaving the terminal screen and
# coming back. The emulator starts empty and `dtach` keeps no screen, so the
# proof is reading, after returning, a line that only exists at the START of
# the output, far above one screen.
#
# `seq` makes every line carry its own number: "line 1" at the top of the
# scrollback can only have come from the history.
set -e
A=/opt/android-sdk/platform-tools/adb
S=${SCRATCH:-/tmp}
NAME=${1:-hist1}
LINES=${2:-3000}

shot() { $A exec-out screencap -p > "$S/$1"; echo "screenshot $1"; }

$A shell am force-stop tech.northwind.vpsm.app
$A shell am start -n tech.northwind.vpsm.app/com.vpsmanager.app.MainActivity >/dev/null
sleep 12

$A shell input tap 74 214;  sleep 2   # drawer
$A shell input tap 254 489; sleep 4   # Terminal

# New session, so it starts alive and empty.
$A shell input tap 440 726; sleep 2
$A shell input text "$NAME"; sleep 1
$A shell input tap 905 716; sleep 10

# Fill the history by typing ON THE GRID (an EOF over the socket kills the
# shell). The pty log only records while a client is attached.
$A shell input tap 540 1200; sleep 3
$A shell input text "seq%s1%s$LINES"; sleep 1
$A shell input keyevent 66; sleep 12
shot "hist_before_leaving.png"

# LEAVE the session (back to the list) and RETURN: the ViewModel and its engine
# are destroyed, and the reattach starts with a blank grid.
$A shell input keyevent 4; sleep 2   # close the keyboard
$A shell input keyevent 4; sleep 5   # back to the session list
shot "hist_list.png"

# Re-enter the first session in the list.
$A shell input tap 540 620; sleep 15
shot "hist_on_return.png"

# Scroll to the start. Each swipe moves about one screen; 60 swipes easily
# cover 3,000 lines on a ~40-line grid.
for _ in $(seq 1 60); do
  $A shell input swipe 540 700 540 1900 120
done
sleep 3
shot "hist_scrollback_top.png"
echo "done: see $S/hist_scrollback_top.png"
