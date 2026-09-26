#!/usr/bin/env bash
# setup-fdroidserver.sh — provisiona o fdroidserver numa venv isolada em
# .tools/fdroidserver-env, sem tocar em nenhum outro ambiente Python da
# máquina. Idempotente: seguro rodar de novo a qualquer momento.
#
# Este script NÃO gera a chave de assinatura do repositório F-Droid — ver
# docs/android-fdroid-repo.md, Seção 1: essa chave é gerada OFFLINE, na
# máquina do operador, exatamente como a chave de assinatura do APK
# (docs/android-signing-keystore.md). Este script cuida só da ferramenta.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

VENV_DIR=".tools/fdroidserver-env"
FDROID_BIN="${VENV_DIR}/bin/fdroid"

if [ -x "$FDROID_BIN" ]; then
  echo "already provisioned: ${FDROID_BIN}"
  exit 0
fi

echo "==> criando venv isolada em ${VENV_DIR}"
mkdir -p .tools
python3 -m venv "$VENV_DIR"

echo "==> instalando fdroidserver (fonte aprovada na Task 1: GitLab oficial)"
"${VENV_DIR}/bin/pip" install --upgrade pip
"${VENV_DIR}/bin/pip" install "git+https://gitlab.com/fdroid/fdroidserver.git"

echo "==> verificando pacotes de sistema exigidos (STACK.md §9)"
missing=()

check_cmd() {
  command -v "$1" >/dev/null 2>&1 || missing+=("comando ausente: $1")
}

check_dpkg() {
  dpkg -s "$1" >/dev/null 2>&1 || missing+=("pacote apt ausente: $1")
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
  echo "AVISO: dependências de sistema ausentes (instalar manualmente, requer root):"
  for m in "${missing[@]}"; do
    echo "  - $m"
  done
  echo ""
  echo "Sugestão: sudo apt install apksigner default-jdk-headless python3-pil \\"
  echo "  python3-pyasn1 python3-pyasn1-modules python3-ruamel.yaml python3-yaml"
  echo ""
fi

echo "✓ fdroidserver provisionado em ${FDROID_BIN}"
echo "  A chave de assinatura do repositório NÃO é gerada aqui — ver"
echo "  docs/android-fdroid-repo.md."
