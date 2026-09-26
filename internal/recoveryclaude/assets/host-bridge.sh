#!/bin/sh
# host-bridge: runs ON THE HOST the binary named after how this was invoked.
# Installed as a symlink under several names (agentctl, graphify, panelctl,
# jira-api) because the project hooks call those host tools.
#
# In an emergency session a missing CONVENIENCE tool must never break anything,
# so the failure path is always "exit 0 silently", never an error.
set -u
NAME="$(basename "$0")"

# Where the binary usually lives ON THE HOST; the first one found wins.
for c in "/usr/local/bin/$NAME" "/root/.local/bin/$NAME" "/usr/bin/$NAME" "/bin/$NAME"; do
  if [ -x "/host$c" ]; then
    TARGET="$c"
    break
  fi
done

if [ -z "${TARGET:-}" ]; then
  # Not on the host: succeed silently.
  exit 0
fi

# nsenter into PID 1's namespaces runs as if on the host. The cwd is kept when
# it exists on both sides (/opt/panel normally), else falls back to /.
DIR="$(pwd 2>/dev/null || echo /)"
[ -d "/host$DIR" ] || [ -d "$DIR" ] || DIR=/
exec nsenter -t 1 -m -u -i -n -p -- /bin/sh -c 'cd "$1" 2>/dev/null || cd /; shift; exec "$@"' _ "$DIR" "$TARGET" "$@"
