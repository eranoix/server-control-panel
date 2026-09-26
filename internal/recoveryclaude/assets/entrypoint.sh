#!/bin/bash
# PID 1 only has to stay up: the work happens in the dtach session the recovery
# screen attaches on demand (docker exec), so a dying `claude` or a closed
# session never takes the container down.
set -euo pipefail
mkdir -p /config
# Own settings.json without ANTHROPIC_BASE_URL: this keeps Claude on a direct
# API connection. An existing file (user edited) is left alone.
if [ ! -f /config/settings.json ]; then
  cat > /config/settings.json <<'JSON'
{
  "env": {
    "CLAUDE_CODE_DISABLE_MOUSE": "1"
  }
}
JSON
fi
echo "[recovery-claude] ready: $(claude --version 2>/dev/null || echo 'claude unavailable')"
if [ -f /config/.credentials.json ]; then
  echo "[recovery-claude] own credential present"
else
  echo "[recovery-claude] NO credential: run 'claude' in the recovery tab and log in once"
fi
exec sleep infinity
