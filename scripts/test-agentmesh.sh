#!/usr/bin/env bash
# suite de teste do agent-mesh anti-clobber.
#
# Roda no HOST CANÔNICO (/opt/panel) — usa o agentctl instalado e a
# toolchain Tailwind, que só existem aqui. NÃO faz deploy real nem reinicia
# serviço: todas as asserções são offline/seguras (guard sem mutar, build de CSS
# num tmpdir, status do git). A corrida de 2 deploys reais é um procedimento
# MANUAL documentado no rodapé (perigoso p/ automatizar — reinicia o serviço).
#
# Uso: scripts/test-agentmesh.sh        (de dentro do repo/worktree)
set -uo pipefail

# Default = host canônico; override p/ rodar contra um worktree antes da ativação.
ROOT="${AGENTMESH_ROOT:-/opt/panel}"
AGENTCTL="$ROOT/.claude/agentctl"
DEPLOY="$ROOT/scripts/deploy.sh"
BUILDTW="$ROOT/scripts/build-tailwind.sh"
pass=0; fail=0
ok()   { echo "  ✓ $1"; pass=$((pass+1)); }
no()   { echo "  ✗ $1"; fail=$((fail+1)); }

echo "═══ test-agentmesh ═══"

# --- A2: deploy.sh cru morre sem AGENTCTL_DEPLOY; ALLOW_RAW_DEPLOY=1 passa o guard
echo "[A2] enforcement no deploy.sh"
out="$(AGENTCTL_DEPLOY=0 ALLOW_RAW_DEPLOY=0 bash "$DEPLOY" /bin/true 2>&1)"
echo "$out" | grep -q 'deploy fora do agent-mesh' && ok "deploy cru bloqueado" || no "deploy cru NÃO bloqueado"
# guard-only (condição), sem deixar prosseguir pra deploy real:
if AGENTCTL_DEPLOY=0 ALLOW_RAW_DEPLOY=1 bash -c '[[ "${AGENTCTL_DEPLOY}" != "1" && "${ALLOW_RAW_DEPLOY}" != "1" ]]'; then
  no "ALLOW_RAW_DEPLOY=1 deveria passar o guard"; else ok "ALLOW_RAW_DEPLOY=1 passa o guard"; fi
# rollback nunca é bloqueado (bloco --rollback precede o guard no arquivo):
gline="$(grep -n 'deploy fora do agent-mesh' "$DEPLOY" | head -1 | cut -d: -f1)"
rline="$(grep -n '== "--rollback"' "$DEPLOY" | head -1 | cut -d: -f1)"
[[ -n "$gline" && -n "$rline" && "$rline" -lt "$gline" ]] && ok "bloco --rollback ($rline) precede o guard ($gline)" || no "ordem rollback/guard incorreta"

# --- A0: build-tailwind gera CSS do ALVO sem tocar outras árvores
echo "[A0] build-tailwind por-worktree"
if [[ -x "$ROOT/scripts/tailwindcss" ]]; then
  tmpd="$(mktemp -d)"; mkdir -p "$tmpd/internal/webassets/web"
  cp "$ROOT/internal/webassets/web/index.html" "$tmpd/internal/webassets/web/" 2>/dev/null
  cp "$ROOT/internal/webassets/web/vendor/vpsm/app/00-shell.js" "$tmpd/internal/webassets/web/" 2>/dev/null
  before="$(md5sum "$ROOT/internal/webassets/web/tailwind.css" | awk '{print $1}')"
  if bash "$BUILDTW" "$tmpd" >/dev/null 2>&1 && [[ -s "$tmpd/internal/webassets/web/tailwind.css" ]]; then
    ok "CSS gerado no alvo ($(stat -c%s "$tmpd/internal/webassets/web/tailwind.css") bytes)"
  else no "build-tailwind não gerou CSS no alvo"; fi
  after="$(md5sum "$ROOT/internal/webassets/web/tailwind.css" | awk '{print $1}')"
  [[ "$before" == "$after" ]] && ok "árvore principal intacta" || no "build tocou a principal!"
  rm -rf "$tmpd"
else echo "  - toolchain ausente, pulando A0"; fi

# --- A1: .claude/sessions ignorado (não suja o tree)
echo "[A1] sessions não rastreadas"
git -C "$ROOT" check-ignore .claude/sessions/probe.md >/dev/null 2>&1 && ok ".claude/sessions/ ignorado" || no ".claude/sessions/ NÃO ignorado"
[[ "$(git -C "$ROOT" ls-files .claude/sessions/ | wc -l)" -eq 0 ]] && ok "0 sessions rastreadas no canônico" || no "ainda há sessions rastreadas"

# --- A3/A4: estrutura do agentctl (lock único + helper + gocheck)
echo "[A3/A4/B3] estrutura do agentctl"
bash -n "$AGENTCTL" && ok "agentctl: sintaxe OK" || no "agentctl: erro de sintaxe"
grep -q 'flock -w 600 8' "$AGENTCTL" && ok "lock único (flock -w 600 8) presente" || no "lock único ausente"
grep -q '_advance_canon_and_publish()' "$AGENTCTL" && ok "helper de avanço/propagação presente" || no "helper ausente"
grep -q 'make build' "$AGENTCTL" && ok "deploy usa make build (tailwind+vpsmctl)" || no "deploy NÃO usa make build"
grep -q 'merge -X theirs' "$AGENTCTL" && no "propagação ainda usa -X theirs (destrutivo)" || ok "propagação não-destrutiva (sem -X theirs)"
grep -q 'go build ./... && go vet ./...' "$AGENTCTL" && ok "gate de backend (go build/vet) presente" || no "gate de backend ausente"

# --- invariantes verdes no canônico
echo "[inv] invariantes no source canônico"
( cd "$ROOT" && AGENTCTL_SKIP_GOCHECK=1 bash "$AGENTCTL" invariant check >/dev/null 2>&1 ) \
  && ok "invariant check verde" || no "invariant check falhou"

# avanço do canônico (regressão de clobber silencioso)
# O teste vive em scripts/test-canon-advance.sh — hermético (só git em tmpdirs),
# então roda também no pre-push e no CI, onde esta suíte não cabe (precisa da
# toolchain Tailwind e do host canônico). Aqui só delegamos, p/ fonte única.
echo "avanço do canônico (delegado a test-canon-advance.sh)"
if bash "$ROOT/scripts/test-canon-advance.sh" "$AGENTCTL" > /tmp/canonadv.$$ 2>&1; then
  ok "avanço do canônico: $(grep -c '✓' /tmp/canonadv.$$) asserções OK"
else
  no "avanço do canônico FALHOU:"; sed 's/^/      /' /tmp/canonadv.$$
fi
rm -f /tmp/canonadv.$$

echo "─────────────────────────────────────"
echo "RESULTADO: $pass OK / $fail FALHAS"
[[ $fail -eq 0 ]] || exit 1

cat <<'MANUAL'

─── PROCEDIMENTO MANUAL (corrida de 2 deploys reais — perigoso p/ automatizar) ───
  Em dois worktrees distintos, um tocando um marcador no index.html e outro um
  .go, rode `agentctl deploy` quase simultâneo. Esperado (graças ao lock único):
    • os deploys SERIALIZAM (deploys.jsonl sem sobreposição temporal);
    • curl localhost:8765/         → contém AMBOS os marcadores (nenhum clobber);
    • curl localhost:8765/tailwind.css | head -c1 | wc -c == 1 (CSS não-vazio);
    • git -C /opt/panel log --oneline -3 refactor/foundation → ambos os commits.
MANUAL
