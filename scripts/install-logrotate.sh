#!/usr/bin/env bash
# Instala /etc/logrotate.d/vps-manager — rotação dos logs do control-plane.
#
# Idempotente: pode rodar várias vezes. Cron diário do logrotate consome.
#
# Rotaciona:
#   - /opt/panel/data/audit.log    — JSONL append-only (eventos de auth/admin)
#   - /opt/panel/data/deploy.log   — log do scripts/deploy.sh
#
# Retém 14 cópias gzipped (~2 semanas), corte em 50MB ou daily (o que vier primeiro).
# notifempty pra não criar arquivos vazios. copytruncate pra não exigir reload
# do binário (audit logger mantém fd aberto; vai escrever no truncado).

set -euo pipefail

DEST=/etc/logrotate.d/vps-manager

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

# Dry-run pra validar sintaxe sem rotacionar.
if command -v logrotate >/dev/null 2>&1; then
    logrotate -d "$DEST" 2>&1 | tail -10
fi
