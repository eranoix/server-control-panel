#!/usr/bin/env bash

set -euo pipefail

DEST=/etc/logrotate.d/server-control-panel

cat >"$DEST" <<'CONF'
/opt/panel/data/audit.log {
    daily
    maxsize 50M
    rotate 14
    compress
    delaycompress
    notifempty
    missingok
    copytruncate
    create 600 root root
}

/opt/panel/data/deploy.log {
    weekly
    maxsize 20M
    rotate 8
    compress
    delaycompress
    notifempty
    missingok
    copytruncate
    create 644 root root
}
CONF

chmod 644 "$DEST"
echo "Installed $DEST"

if command -v logrotate >/dev/null 2>&1; then
    logrotate -d "$DEST" 2>&1 | tail -10
fi
