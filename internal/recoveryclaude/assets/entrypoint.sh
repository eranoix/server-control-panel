#!/bin/bash
set -euo pipefail
mkdir -p /config
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
