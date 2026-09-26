#!/usr/bin/env bash
# scripts/whatsapp/uninstall.sh — removes the WAHA gateway from this host.
# By default keeps data (session blob, message media) so re-install resumes
# without re-pairing. Pass --purge to wipe data too.
#
# WARNING: --purge deletes /var/lib/vpsm-whatsapp entirely (sessions + media).
# After --purge the device has to scan a fresh QR.

set -euo pipefail

if [ "${EUID:-$(id -u)}" -ne 0 ]; then
  echo "this script must run as root" >&2
  exit 1
fi

PURGE=0
if [ "${1:-}" = "--purge" ]; then PURGE=1; fi

systemctl stop vpsm-whatsapp.service 2>/dev/null || true
systemctl disable vpsm-whatsapp.service 2>/dev/null || true

rm -f /etc/systemd/system/vpsm-whatsapp.service
systemctl daemon-reload

if [ $PURGE -eq 1 ]; then
  rm -rf /var/lib/vpsm-whatsapp
  rm -rf /opt/panel/data/whatsapp
  echo "purged data dirs"
else
  echo "preserved /var/lib/vpsm-whatsapp and data/whatsapp (use --purge to remove)"
fi

rm -rf /opt/vpsm-whatsapp

echo "WAHA gateway uninstalled."
