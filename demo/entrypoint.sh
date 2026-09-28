#!/bin/sh
set -eu
case "$(printf %s "${DEMO_MODE:-}" | tr "[:upper:]" "[:lower:]")" in
  ""|0|false) ;;
  *) cp -r /app/seed/. /app/data/ 2>/dev/null || true ;;
esac
exec server-control-panel "$@"
