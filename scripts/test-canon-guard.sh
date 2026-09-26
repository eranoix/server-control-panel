#!/usr/bin/env bash
# test-canon-guard.sh — o canônico não se move em silêncio.
#
# O QUE ESTE TESTE PROTEGE
# Avançar o canônico é a operação de maior alcance do projeto: a partir dali,
# TODO deploy de TODA sessão passa a incluir o que foi promovido. `git fetch .
# <branch>:refactor/foundation` faz isso sem imprimir uma linha — quem roda não
# sabe se promoveu 1 commit ou 50. Aconteceu: um sync de rotina levou junto ~50
# commits de outra sessão.
#
# POR QUE NÃO BASTA VERIFICAR DEPOIS (como nos dois testes anteriores): quando o
# resultado aparece, a promoção já ocorreu. A barreira tem que vir ANTES, e fora
# do agentctl — quem usa git cru não passa por ele. Daí o hook.
#
# Uso: scripts/test-canon-guard.sh [caminho-do-agentctl]
set -uo pipefail

# ─── ISOLAMENTO DO AMBIENTE GIT (não remova) ────────────────────────────────
# Sob o pre-push o git exporta GIT_DIR e cia, que sobrepõem a descoberta de
# repositório e vencem até o `git -C`: sem limpar, as fixtures escreveriam no
# repositório REAL (estrago concreto, já visto uma vez).
for _v in $(env | sed -n 's/^\(GIT_[A-Za-z0-9_]*\)=.*/\1/p'); do unset "$_v"; done
unset _v
# Herdar a sanção do processo pai faria o teste do bloqueio passar por engano.
unset AGENTCTL_CANON ALLOW_RAW_CANON 2>/dev/null || true

RAIZ="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
AGENTCTL="${1:-$RAIZ/.claude/agentctl}"
HOOK="$RAIZ/scripts/hooks/reference-transaction.sh"
[ -f "$AGENTCTL" ] || { echo "não achei o agentctl em $AGENTCTL"; exit 2; }
[ -f "$HOOK" ] || { echo "não achei o hook em $HOOK"; exit 2; }

pass=0; fail=0
ok() { echo "  ✓ $1"; pass=$((pass+1)); }
no() { echo "  ✗ $1"; fail=$((fail+1)); }
echo "=== test-canon-guard ==="

TMP="$(mktemp -d "${TMPDIR:-/tmp}/vpsm-guard.XXXXXX")" || exit 2
trap 'case "$TMP" in "${TMPDIR:-/tmp}"/vpsm-guard.*) rm -rf "$TMP";; esac' EXIT

CANON="refactor/foundation"

mk_repo() { # repo com canônico, hook instalado e uma branch de trabalho
  local d="$1" n="${2:-1}" i
  mkdir -p "$d/.claude/coord"
  git -C "$d" init -q -b "$CANON"
  git -C "$d" config user.email t@t; git -C "$d" config user.name t
  git -C "$d" config commit.gpgsign false
  echo base > "$d/f.txt"; git -C "$d" add -A >/dev/null 2>&1; git -C "$d" commit -q -m base
  mkdir -p "$d/.git/hooks"; ln -sf "$HOOK" "$d/.git/hooks/reference-transaction"
  git -C "$d" checkout -q -b feat/trabalho
  for i in $(seq 1 "$n"); do
    echo "linha $i" >> "$d/f.txt"; git -C "$d" add -A >/dev/null 2>&1
    git -C "$d" commit -q -m "trabalho $i"
  done
  local top; top="$(git -C "$d" rev-parse --show-toplevel 2>/dev/null || true)"
  [ "$top" = "$(cd "$d" && pwd -P)" ] || { echo "  ✗ ABORTADO: fixture não é o repo alvo (GIT_DIR vazando?)"; exit 3; }
}
canon_em() { git -C "$1" rev-parse "$CANON"; }
sync_cmd() { local d="$1"; shift; VPSM_ROOT="$d" VPSM_CANON="$CANON" bash "$AGENTCTL" canon-sync "$@" 2>&1; }

# ── 1. git cru é BARRADO, e a barreira explica o alcance ────────────────────
echo "[1] movimento cru do canônico"
A="$TMP/a"; mk_repo "$A" 3
antes="$(canon_em "$A")"
saida="$(git -C "$A" fetch . "feat/trabalho:$CANON" 2>&1)"
[ "$(canon_em "$A")" = "$antes" ] \
  && ok "git fetch cru NÃO moveu o canônico" \
  || no "CRÍTICO: o canônico foi movido por git cru — o guard não está ativo"
echo "$saida" | grep -q "BLOQUEADO" && ok "a recusa é explícita" || no "recusou sem explicar: $saida"
echo "$saida" | grep -q "promoveria 3 commit" \
  && ok "a recusa DIZ QUANTOS commits seriam promovidos (o dado que faltava)" \
  || no "a recusa não informou o tamanho do delta"
echo "$saida" | grep -q "canon-sync" && ok "aponta o caminho sancionado" || no "não aponta a alternativa"

# Apagar o canônico também não passa.
git -C "$A" checkout -q feat/trabalho
saida="$(git -C "$A" branch -D "$CANON" 2>&1)"
git -C "$A" rev-parse --verify --quiet "$CANON" >/dev/null \
  && ok "apagar o canônico é barrado" \
  || no "CRÍTICO: o canônico foi APAGADO por comando cru"

# ── 2. o caminho sancionado funciona ────────────────────────────────────────
echo "[2] agentctl canon-sync"
B="$TMP/b"; mk_repo "$B" 2
tip="$(git -C "$B" rev-parse feat/trabalho)"
saida="$(sync_cmd "$B" feat/trabalho)"
git -C "$B" merge-base --is-ancestor "$tip" "$CANON" \
  && ok "canon-sync avança o canônico" \
  || no "canon-sync não avançou: $saida"
echo "$saida" | grep -q "promove 2 commit" \
  && ok "lista o delta ANTES de agir" \
  || no "não mostrou o delta"
echo "$saida" | grep -q "✓ $CANON" && ok "confirma o pós-estado" || no "não confirmou o pós-estado"

# ── 3. delta grande exige confirmação ───────────────────────────────────────
# É o caso que originou o ticket: dezenas de commits entrando sem ninguém decidir.
echo "[3] delta grande"
C="$TMP/c"; mk_repo "$C" 20
antes="$(canon_em "$C")"
saida="$(sync_cmd "$C" feat/trabalho)"
[ "$(canon_em "$C")" = "$antes" ] \
  && ok "delta de 20 commits NÃO é promovido sem confirmação" \
  || no "CRÍTICO: promoveu 20 commits sem perguntar — é exatamente o bug do ticket"
echo "$saida" | grep -q -- "--yes" && ok "diz como confirmar" || no "não explica como prosseguir"
saida="$(sync_cmd "$C" feat/trabalho --yes)"
git -C "$C" merge-base --is-ancestor "$(git -C "$C" rev-parse feat/trabalho)" "$CANON" \
  && ok "com --yes, promove" \
  || no "--yes não funcionou: $saida"

# ── 4. não-fast-forward é recusado ──────────────────────────────────────────
# Avanço não-FF reescreveria trabalho alheio — pior que promover demais.
echo "[4] não-fast-forward"
D="$TMP/d"; mk_repo "$D" 1
git -C "$D" checkout -q "$CANON"
echo divergente >> "$D/f.txt"; git -C "$D" add -A >/dev/null 2>&1
AGENTCTL_CANON=1 git -C "$D" commit -q -m "commit só no canônico"
antes="$(canon_em "$D")"
saida="$(sync_cmd "$D" feat/trabalho)"
[ "$(canon_em "$D")" = "$antes" ] \
  && ok "recusa avanço não-fast-forward (não reescreve trabalho alheio)" \
  || no "CRÍTICO: reescreveu o canônico"

# ── 5. o guard é cirúrgico: outras branches seguem livres ───────────────────
echo "[5] escopo"
E="$TMP/e"; mk_repo "$E" 1
git -C "$E" branch outra "$CANON" 2>/dev/null
git -C "$E" fetch . feat/trabalho:outra >/dev/null 2>&1
[ "$(git -C "$E" rev-parse outra)" = "$(git -C "$E" rev-parse feat/trabalho)" ] \
  && ok "branches que não são o canônico continuam livres" \
  || no "o guard está bloqueando branch comum — atrapalharia o trabalho normal"
ALLOW_RAW_CANON=1 git -C "$E" fetch . feat/trabalho:"$CANON" >/dev/null 2>&1
git -C "$E" merge-base --is-ancestor "$(git -C "$E" rev-parse feat/trabalho)" "$CANON" \
  && ok "ALLOW_RAW_CANON=1 libera o bypass consciente" \
  || no "o escape de emergência não funciona"

echo
echo "RESULTADO: $pass OK / $fail FALHAS"
[ "$fail" -eq 0 ]
