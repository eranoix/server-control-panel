#!/bin/bash
set -euo pipefail

A=${ADB:-/opt/android-sdk/platform-tools/adb}
PKG=tech.northwind.servercontrolpanel
S=${SCRATCH:-/tmp}
SOURCE=${1:-}
DATA_DIR=${PANEL_DATA_DIR:-/opt/panel/data}
INBOX="$DATA_DIR/mobile-inbox"
STAGING="$DATA_DIR/.mobile-upload-staging"

if [ -z "$SOURCE" ]; then
  SOURCE="$S/test attachment.txt"
  head -c 300000 /dev/urandom | base64 > "$SOURCE"
fi
REMOTE_NAME="$(basename "$SOURCE")"
SOURCE_SHA="$(sha256sum "$SOURCE" | cut -d' ' -f1)"

echo "== source: $SOURCE ($(stat -c%s "$SOURCE") bytes, sha256 ${SOURCE_SHA:0:12}…)"

echo "== 1. seeding the file where the system picker can see it =="
$A push "$SOURCE" "/sdcard/Download/$REMOTE_NAME" >/dev/null

echo "== 2. opening the terminal in a known state =="
$A logcat -c
$A shell am force-stop $PKG || true
$A shell am start -n $PKG/dev.servercontrolpanel.app.MainActivity >/dev/null
sleep 12
$A shell input tap 74 214;  sleep 2
$A shell input tap 254 489; sleep 5

echo
echo "== 3. YOUR TURN (the file picker belongs to the SYSTEM, not the app) =="
echo "   tap the paperclip on the key bar → pick '$REMOTE_NAME' in Downloads"
echo "   → wait for the progress strip → tap to insert."
echo "   Meanwhile this script watches both sides."
echo
read -r -p "   press ENTER once inserted (or Ctrl-C to abort) " _

echo "== 4. did the file arrive WHOLE? =="
if [ ! -f "$INBOX/$REMOTE_NAME" ]; then
  echo "FAILED: $INBOX/$REMOTE_NAME does not exist" >&2
  ls -la "$INBOX" || true
  exit 1
fi
DEST_SHA="$(sha256sum "$INBOX/$REMOTE_NAME" | cut -d' ' -f1)"
if [ "$SOURCE_SHA" != "$DEST_SHA" ]; then
  echo "FAILED: sha256 differs: source $SOURCE_SHA, destination $DEST_SHA" >&2
  exit 1
fi
echo "OK: identical bytes"

echo "== 5. is the staging area clean? =="
if [ -d "$STAGING" ] && [ -n "$(ls -A "$STAGING" 2>/dev/null)" ]; then
  echo "WARNING: leftovers in $STAGING:" >&2
  ls -la "$STAGING" >&2
else
  echo "OK: staging empty"
fi

echo "== 6. was the path TYPED into the session? =="
echo "   (check the screenshot; the path must be quoted as ONE argument)"
$A exec-out screencap -p > "$S/attachment-inserted.png"
echo "   screenshot: $S/attachment-inserted.png"

echo "== 7. did the worker complain about anything? =="
$A logcat -d | grep -Ei "WM-|AttachmentUpload|Could not create Worker|TransferRepository" | tail -15 || echo "   (nothing)"

echo
echo "Four cases that have failed before and deserve a run each:"
echo "  a) socket dead before inserting  → must warn and KEEP the line"
echo "  b) name with a space             → must arrive quoted, as a single argument"
echo "  c) airplane mode mid-upload      → must resume at the confirmed byte"
echo "  d) empty file                    → 'The file is empty', never a 413"
