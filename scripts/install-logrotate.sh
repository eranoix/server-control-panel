#!/usr/bin/env bash
# Installs /etc/logrotate.d/server-control-panel: rotation of the control-plane logs.
# Idempotent.
#
# Rotates:
#   - /opt/panel/data/audit.log    append-only JSONL (auth/admin events)
#   - /opt/panel/data/deploy.log   log of the deploy script
#
# copytruncate because the audit logger keeps its fd open; no binary reload needed.

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

# Dry run to validate the syntax without rotating.
if command -v logrotate >/dev/null 2>&1; then
    logrotate -d "$DEST" 2>&1 | tail -10
fi
