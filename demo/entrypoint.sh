#!/bin/sh
# Seed the data directory on every start, in demo mode only.
#
# In the demo /app/data is a tmpfs, so it comes up empty each time and this
# restores the fabricated state. The effect is a demo that resets itself:
# nothing a visitor does survives a restart, and the reset needs no cron, no
# cleanup job and no privileged access.
#
# Outside the demo the seed must NOT be copied. It carries the demo account
# (demo / demo, admin) and a JWT secret that is public in this repository;
# copied over a real data directory on every start, it would replace the
# generated admin password and undo any credential change at the next restart.
# Without it, the first start writes a fresh config.json and a random admin
# password to INITIAL_CREDENTIALS.txt.
#
# Same truthiness as the server itself (internal/api/demo_mode.go): set, and
# neither "0" nor "false".
set -eu
case "$(printf %s "${DEMO_MODE:-}" | tr "[:upper:]" "[:lower:]")" in
  ""|0|false) ;;
  *) cp -r /app/seed/. /app/data/ 2>/dev/null || true ;;
esac
exec server-control-panel "$@"
