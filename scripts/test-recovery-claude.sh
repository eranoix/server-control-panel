#!/usr/bin/env bash
# test-recovery-claude.sh — o Claude da tela de recuperação tem de ser
# INDEPENDENTE, e independência é uma propriedade fácil de perder sem perceber.
#
# Basta alguém acrescentar um ANTHROPIC_BASE_URL "para padronizar", ou montar o
# .credentials.json do host "para não precisar autenticar duas vezes", e o
# container passa a depender exatamente daquilo que ele existe para contornar —
# sem que nada quebre visivelmente. Por isso as garantias são afirmadas aqui.
#
# As checagens estáticas rodam em qualquer lugar (CI incluso). As de execução só
# rodam onde o container existe, e a ausência dele NÃO reprova: o CI não tem
# Docker do host, e fingir cobertura seria pior que não ter.
set -uo pipefail
RAIZ="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$RAIZ"

pass=0; fail=0
ok()  { printf 'PASS %s\n' "$1"; pass=$((pass+1)); }
no()  { printf 'FAIL %s\n' "$1"; fail=$((fail+1)); }
pula(){ printf 'SKIP %s\n' "$1"; }

echo "=== test-recovery-claude ==="

DOCKERFILE="internal/recoveryclaude/assets/Dockerfile"
SCRIPT="internal/recoveryclaude/assets/manage.sh"
ENTRY="internal/recoveryclaude/assets/entrypoint.sh"
HANDLER="internal/api/handlers_recovery.go"

for f in "$DOCKERFILE" "$SCRIPT" "$ENTRY" "$HANDLER"; do
  [ -f "$f" ] || { no "arquivo ausente: $f"; }
done

# ── 1. a garantia central: nada aponta este Claude para o claude-router ──────
if grep -q 'ANTHROPIC_BASE_URL' "$DOCKERFILE" "$ENTRY" 2>/dev/null | grep -qv '^\s*#'; then
  no "ANTHROPIC_BASE_URL aparece na imagem — o container voltou a passar pelo router"
else
  # comentários explicando a ausência são bem-vindos; o que não pode é ENV/export.
  if grep -E '^(ENV|export)[[:space:]]+ANTHROPIC_BASE_URL' "$DOCKERFILE" "$ENTRY" >/dev/null 2>&1; then
    no "ANTHROPIC_BASE_URL definida na imagem — a independência acabou"
  else
    ok "imagem não define ANTHROPIC_BASE_URL (fora do claude-router)"
  fi
fi
if grep -E '(^|[[:space:]])-e[[:space:]]+ANTHROPIC_BASE_URL' "$SCRIPT" >/dev/null 2>&1; then
  no "o script de subida injeta ANTHROPIC_BASE_URL"
else
  ok "o script de subida não injeta ANTHROPIC_BASE_URL"
fi

# ── 2. login PRÓPRIO: não pode emprestar a credencial do host ────────────────
# Os dois lados renovariam o mesmo refresh token e se invalidariam — a
# ferramenta de emergência passaria a poder derrubar o Claude principal.
if grep -E '/root/\.claude|\.credentials\.json' "$SCRIPT" | grep -E '^\s*[^#]*-v ' >/dev/null 2>&1; then
  no "o container monta credencial do host — os dois refresh tokens se invalidam"
else
  ok "não monta credencial do host (login próprio)"
fi
if grep -q 'VOLUME \["/config"\]' "$DOCKERFILE" && grep -q '\$VOLUME:/config' "$SCRIPT"; then
  ok "config-dir é volume nomeado (o login sobrevive a rebuild e docker rm)"
else
  no "config-dir não é volume — o login se perderia no primeiro rebuild"
fi

# ── 3. independente do vps-manager: sobe pelo Docker, no boot ────────────────
if grep -q -- '--restart always' "$SCRIPT"; then
  ok "restart always (sobe no boot sem depender de nada nosso)"
else
  no "sem restart always — o container não voltaria sozinho"
fi

# ── 4. a porta de entrada continua gated pela auth de recuperação ────────────
if grep -A 6 'func (r \*Router) handleRecoveryClaudePTY' "$HANDLER" | grep -q 'recoveryUserFromCookie'; then
  ok "o terminal do Claude exige a sessão de recuperação"
else
  no "handleRecoveryClaudePTY sem checagem de sessão — rota aberta"
fi
if grep -A 6 'func (r \*Router) handleRecoveryClaudeStatus' "$HANDLER" | grep -q 'recoveryUserFromCookie'; then
  ok "o status do container exige a sessão de recuperação"
else
  no "handleRecoveryClaudeStatus sem checagem de sessão"
fi
if grep -q '/recovery/ws/claude' internal/api/api.go && grep -q '/recovery/claude/status' internal/api/api.go; then
  ok "rotas registradas"
else
  no "rotas do Claude de recuperação não estão registradas"
fi

# ── 5. `container inspect`, nunca `inspect` ─────────────────────────────────
# A imagem tem o MESMO nome do container. `docker inspect` casa os dois e hoje
# resolve o container primeiro — por sorte, não por contrato. Essa ambiguidade
# já fez o `up` tentar `docker start` num container que não existia.
if grep -nE '^[^#]*docker (container )?inspect' "$SCRIPT" | grep -vq 'container inspect'; then
  no "o script usa 'docker inspect' (casa imagem também) em vez de 'container inspect'"
else
  ok "script usa 'docker container inspect' (sem ambiguidade com a imagem)"
fi
if grep -A 2 '"/usr/bin/docker"' "$HANDLER" | grep -q '"inspect"' && ! grep -A 1 '"/usr/bin/docker"' "$HANDLER" | grep -q '"container", "inspect"'; then
  no "o handler usa 'docker inspect' em vez de 'container inspect'"
else
  ok "handler usa 'docker container inspect'"
fi

# ── 6. o binário tem de carregar o que precisa ──────────────────────────────
# Duas tentativas anteriores falharam pela mesma raiz, e as duas ficam barradas
# aqui: (1) apontar para um script do repositório — o deploy builda do worktree
# do ticket enquanto /opt/panel está noutra branch; (2) fazer o deploy
# instalar o script — o agentctl executa o deploy.sh do working tree PRINCIPAL,
# então um passo novo no deploy.sh de um worktree nunca roda.
if grep -qE '"/opt/panel/scripts/|/usr/local/bin/vpsm-recovery-claude' "$HANDLER"; then
  no "o handler voltou a depender de um script em caminho fixo do disco"
else
  ok "handler não depende de script em caminho fixo (nem repo, nem /usr/local/bin)"
fi
if grep -q 'recoveryclaude.Comando' "$HANDLER"; then
  ok "handler materializa o gerenciador EMBUTIDO no binário (viaja com o deploy)"
else
  no "handler não usa o gerenciador embutido — volta a depender do disco"
fi
if grep -q 'go:embed assets' internal/recoveryclaude/recoveryclaude.go; then
  ok "Dockerfile e scripts estão embutidos no binário"
else
  no "assets não estão embutidos — o binário não carrega o que precisa"
fi
# O contexto de build tem de ser o diretório do próprio script: é o que faz o
# mesmo arquivo funcionar no repo E materializado em <DataDir>.
if grep -q 'CTX="$(cd "$(dirname "${BASH_SOURCE\[0\]}")" && pwd)"' "$SCRIPT"; then
  ok "contexto de build é o diretório do próprio script (funciona nos dois lugares)"
else
  no "contexto de build aponta pra fora — quebra quando materializado"
fi

# ── 7. sessão nomeada: a conversa sobrevive a uma reconexão ──────────────────
# O que importa é a SESSÃO NOMEADA e o "anexa se existir, cria se não" — não a
# ferramenta. Passou a ser dtach em 2026-09-12, para não haver dois
# multiplexadores no produto; a propriedade protegida é a mesma.
if grep -q 'dtach -A /tmp/recovery.sock' "$HANDLER"; then
  ok "anexa a uma sessão dtach nomeada (reconectar não perde a conversa)"
else
  no "sem sessão nomeada — cada reconexão começaria do zero"
fi

# ── 8. execução (só onde o container existe) ────────────────────────────────
if ! command -v docker >/dev/null 2>&1; then
  pula "verificações de execução: docker ausente neste ambiente"
elif ! docker inspect vpsm-recovery-claude >/dev/null 2>&1; then
  pula "verificações de execução: container ainda não criado (scripts/recovery-claude.sh up)"
else
  bash "$SCRIPT" doctor >/dev/null 2>&1
  case $? in
    0) ok "doctor do container passou (sem router, login próprio, alcance de host)" ;;
    # 2 = estrutura de pé, falta só o login. É passo manual (device flow, uma
    # vez), não regressão — reprovar aqui transformaria "ainda não configurei"
    # em "quebrei alguma coisa", e o CI passaria a mentir.
    2) pula "doctor: estrutura de pé; falta o login manual (rode 'claude' na aba do /recovery)" ;;
    *) no "doctor do container reprovou — rode: $SCRIPT doctor" ;;
  esac
fi

echo "─────────────────────────────────────"
echo "RESULTADO: $pass OK / $fail FALHAS"
[ "$fail" = "0" ]
