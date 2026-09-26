#!/bin/bash
# O processo 1 do container so precisa ficar de pe: quem trabalha aqui e a
# sessao dtach que a tela de recuperacao anexa sob demanda (docker exec). Manter
# o multiplexador fora do PID 1 e de proposito — assim um `claude` que morra, ou uma
# sessao encerrada, nao derruba o container inteiro.
set -euo pipefail
mkdir -p /config
# settings.json proprio, SEM ANTHROPIC_BASE_URL: e isto que tira este Claude do
# claude-router. Se o arquivo ja existir (o usuario mexeu), respeita.
if [ ! -f /config/settings.json ]; then
  cat > /config/settings.json <<'JSON'
{
  "env": {
    "CLAUDE_CODE_DISABLE_MOUSE": "1"
  }
}
JSON
fi
echo "[recovery-claude] pronto: $(claude --version 2>/dev/null || echo 'claude indisponivel')"
if [ -f /config/.credentials.json ]; then
  echo "[recovery-claude] credencial propria presente"
else
  echo "[recovery-claude] SEM credencial — rode 'claude' na aba de recuperacao e faca o login uma vez"
fi
exec sleep infinity
