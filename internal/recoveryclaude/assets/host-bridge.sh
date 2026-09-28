#!/bin/sh
set -u
NAME="$(basename "$0")"

for c in "/usr/local/bin/$NAME" "/root/.local/bin/$NAME" "/usr/bin/$NAME" "/bin/$NAME"; do
  if [ -x "/host$c" ]; then
    TARGET="$c"
    break
  fi
done

if [ -z "${TARGET:-}" ]; then
  exit 0
fi

DIR="$(pwd 2>/dev/null || echo /)"
[ -d "/host$DIR" ] || [ -d "$DIR" ] || DIR=/
exec nsenter -t 1 -m -u -i -n -p -- /bin/sh -c 'cd "$1" 2>/dev/null || cd /; shift; exec "$@"' _ "$DIR" "$TARGET" "$@"
