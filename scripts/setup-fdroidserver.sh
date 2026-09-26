#!/usr/bin/env bash
# setup-fdroidserver.sh: installs fdroidserver in an isolated venv under
# .tools/fdroidserver-env without touching any other Python environment.
# Idempotent: safe to run again at any time.
#
# This script does NOT generate the F-Droid repository signing key: that key is
# generated OFFLINE on the operator's machine, like the APK signing key (see
# docs/android-fdroid-repo.md, section 1). This only provisions the tool.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

VENV_DIR=".tools/fdroidserver-env"
FDROID_BIN="${VENV_DIR}/bin/fdroid"

if [ -x "$FDROID_BIN" ]; then
  echo "already provisioned: ${FDROID_BIN}"
  exit 0
fi

echo "==> creating an isolated venv in ${VENV_DIR}"
mkdir -p .tools
python3 -m venv "$VENV_DIR"

echo "==> installing fdroidserver (from the official GitLab repository)"
"${VENV_DIR}/bin/pip" install --upgrade pip
"${VENV_DIR}/bin/pip" install "git+https://gitlab.com/fdroid/fdroidserver.git"

echo "==> checking required system packages"
missing=()

check_cmd() {
  command -v "$1" >/dev/null 2>&1 || missing+=("missing command: $1")
}

check_dpkg() {
  dpkg -s "$1" >/dev/null 2>&1 || missing+=("missing apt package: $1")
}

check_cmd apksigner
check_dpkg default-jdk-headless
check_dpkg python3-pil
check_dpkg python3-pyasn1
check_dpkg python3-pyasn1-modules
check_dpkg python3-ruamel.yaml
check_dpkg python3-yaml

if [ "${#missing[@]}" -gt 0 ]; then
  echo ""
  echo "WARNING: missing system dependencies (install them by hand, needs root):"
  for m in "${missing[@]}"; do
    echo "  - $m"
  done
  echo ""
  echo "Suggestion: sudo apt install apksigner default-jdk-headless python3-pil \\"
  echo "  python3-pyasn1 python3-pyasn1-modules python3-ruamel.yaml python3-yaml"
  echo ""
fi

echo "✓ fdroidserver provisioned at ${FDROID_BIN}"
echo "  The repository signing key is NOT generated here; see"
echo "  docs/android-fdroid-repo.md."
