#!/usr/bin/env bash
# test-invariant-canon.sh — regressão do `agentctl invariant add`
# gravava o invariante na branch ERRADA e anunciava sucesso.
#
# O BUG: cmd_invariant rodava `git -C "$ROOT" commit`, e `commit` age na branch
# CHECKED-OUT do diretório. O $ROOT (/opt/panel) vive numa branch de
# feature, então o invariante ia parar lá — enquanto a mensagem dizia
# "commitado no canônico", sem nunca conferir. É a reincidência exata do
# Mesmo arquivo, mesmo motivo: confiar no exit code de um
# comando git que age na branch corrente.
#
# POR QUE IMPORTA: o gate lê o invariants.txt EM DISCO, então nada regride na
# hora — o estrago é diferido. Descartada a branch de feature, o registro do
# invariante some do histórico e a proteção vai junto, em silêncio. Um teste que
# só checasse "o comando saiu 0" passaria com o bug de pé; por isso cada
# asserção aqui pergunta ao CANÔNICO o que ele realmente contém.
#
# Uso: scripts/test-invariant-canon.sh [caminho-do-agentctl]
set -uo pipefail

# ─── ISOLAMENTO DO AMBIENTE GIT (não remova) ────────────────────────────────
# Sob o pre-push o git exporta GIT_DIR e cia, que SOBREPÕEM a descoberta de
# repositório e vencem até o `git -C`. Sem limpar, os repos de fixture abaixo
# reinicializariam o repositório REAL (estrago concreto, já visto uma vez).
for _v in $(env | sed -n 's/^\(GIT_[A-Za-z0-9_]*\)=.*/\1/p'); do unset "$_v"; done
unset _v

AGENTCTL="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/.claude/agentctl}"
[ -f "$AGENTCTL" ] || { echo "não achei o agentctl em $AGENTCTL"; exit 2; }

pass=0; fail=0
ok() { echo "  ✓ $1"; pass=$((pass+1)); }
no() { echo "  ✗ $1"; fail=$((fail+1)); }
echo "=== test-invariant-canon ==="

TMP="$(mktemp -d "${TMPDIR:-/tmp}/vpsm-inv-test.XXXXXX")" || exit 2
trap 'case "$TMP" in /tmp/vpsm-inv-test.*|"${TMPDIR:-/tmp}"/vpsm-inv-test.*) rm -rf "$TMP";; esac' EXIT

CANON="refactor/foundation"

# Repo de fixture: canônico com um invariants.txt rastreado + branch de feature.
mk_repo() {
  local d="$1"
  mkdir -p "$d/.claude/coord"
  git -C "$d" init -q -b "$CANON"
  git -C "$d" config user.email t@t; git -C "$d" config user.name t
  git -C "$d" config commit.gpgsign false
  printf 'arquivo.go|simboloBase|1|999|invariante base|-\n' > "$d/.claude/coord/invariants.txt"
  # Espelha o .gitignore real: o agentctl cria board.json/messages.jsonl/
  # deploys.jsonl/.lock ao iniciar, e no repo de verdade eles são estado de
  # runtime ignorado. Sem isto o fixture acusaria "tree sujo" por artefato do
  # próprio fixture, escondendo se o código realmente suja o tree.
  cat > "$d/.gitignore" <<'IGN'
.claude/coord/board.json
.claude/coord/messages.jsonl
.claude/coord/deploys.jsonl
.claude/coord/*.cursor
.claude/coord/.lock
.claude/coord/.deploylock
IGN
  git -C "$d" add -A >/dev/null 2>&1
  git -C "$d" commit -q -m base
  # Isolamento furou? Abortar é melhor que escrever no repo errado.
  local top; top="$(git -C "$d" rev-parse --show-toplevel 2>/dev/null || true)"
  [ "$top" = "$(cd "$d" && pwd -P)" ] || { echo "  ✗ ABORTADO: fixture não é o repo alvo (GIT_DIR vazando?)"; exit 3; }
}

add_inv() {  # add_inv <root> <descrição>
  VPSM_ROOT="$1" VPSM_CANON="$CANON" bash "$AGENTCTL" invariant add \
    "arquivo.go" "simboloNovo" 1 999 "$2" - 2>&1
}

no_canon() {  # a linha existe no invariants.txt DO CANÔNICO?
  git -C "$1" show "$CANON:.claude/coord/invariants.txt" 2>/dev/null | grep -q "$2"
}

# ── A. $ROOT numa branch de feature, canônico em nenhum worktree ────────────
# É a topologia real do host — e a que produzia o bug.
echo "[A] \$ROOT numa branch de feature (topologia real do host)"
A="$TMP/a"; mk_repo "$A"
git -C "$A" checkout -q -b feat/qualquer
out="$(add_inv "$A" "invariante do caso A")"

no_canon "$A" "invariante do caso A" \
  && ok "o invariante foi parar NO CANÔNICO (era o bug: ia pra branch de feature)" \
  || no "invariante NÃO chegou no canônico — o bug voltou"

echo "$out" | grep -q "commitado no canônico" \
  && ok "anuncia sucesso" || no "não anunciou sucesso: $out"

[ -z "$(git -C "$A" status --porcelain)" ] \
  && ok "tree do \$ROOT continua limpo (tree sujo sabota a convergência)" \
  || no "deixou o tree do \$ROOT sujo: $(git -C "$A" status --porcelain | head -2)"

[ "$(git -C "$A" symbolic-ref --short HEAD)" = "feat/qualquer" ] \
  && ok "não trocou a branch do \$ROOT por baixo do usuário" \
  || no "a branch do \$ROOT mudou"

# Divergência é o custo escondido de "commitar nos dois lugares": duas commits
# gêmeas separam as branches pra sempre e todo alinhamento futuro vira merge.
# Sendo o $ROOT ancestral do canônico, o certo é ADOTAR a commit por FF.
git -C "$A" merge-base --is-ancestor HEAD "$CANON" \
  && ok "\$ROOT adotou a commit do canônico por FF (sem branches divergindo)" \
  || no "\$ROOT divergiu do canônico — duas commits gêmeas, alinhamento vira merge"
[ "$(git -C "$A" rev-list --count HEAD)" = "2" ] \
  && ok "uma única commit no total (não duplicou o registro)" \
  || no "gerou $(git -C "$A" rev-list --count HEAD) commits, esperava 2"

grep -q "invariante do caso A" "$A/.claude/coord/invariants.txt" \
  && ok "arquivo em disco (o que o gate lê) tem o invariante" \
  || no "arquivo em disco ficou sem o invariante — o gate não protegeria"

# ── B. $ROOT já está no canônico ────────────────────────────────────────────
echo "[B] \$ROOT já está no canônico"
B="$TMP/b"; mk_repo "$B"
add_inv "$B" "invariante do caso B" >/dev/null
no_canon "$B" "invariante do caso B" \
  && ok "grava no canônico" || no "não gravou no canônico"
[ -z "$(git -C "$B" status --porcelain)" ] \
  && ok "tree limpo" || no "tree sujo"
[ "$(git -C "$B" rev-list --count HEAD)" = "2" ] \
  && ok "uma única commit (não duplica quando já se está no canônico)" \
  || no "gerou $(git -C "$B" rev-list --count HEAD) commits, esperava 2"

# ── C. canônico CHECKED-OUT noutro worktree ─────────────────────────────────
# Mover a ref por baixo de um worktree deixaria a sessão dele com um diff
# reverso fantasma — todo arquivo da commit aparecendo como "modificado".
echo "[C] canônico checked-out noutro worktree"
C="$TMP/c"; mk_repo "$C"
git -C "$C" checkout -q -b feat/outra
git -C "$C" worktree add -q "$TMP/c-canon" "$CANON" 2>/dev/null
out="$(add_inv "$C" "invariante do caso C")"
no_canon "$C" "invariante do caso C" \
  && ok "grava no canônico mesmo com ele checked-out" || no "não gravou no canônico"
[ -z "$(git -C "$TMP/c-canon" status --porcelain)" ] \
  && ok "o worktree do canônico NÃO ficou com diff fantasma" \
  || no "sujou o worktree alheio: $(git -C "$TMP/c-canon" status --porcelain | head -2)"
[ -z "$(git -C "$C" status --porcelain)" ] \
  && ok "tree do \$ROOT limpo" || no "tree do \$ROOT sujo"

# ── D. honestidade quando NÃO dá pra gravar ─────────────────────────────────
# A regressão que este e o teste anterior têm em comum não é "falhou": é
# "falhou e disse que deu certo". Sem canônico, tem que avisar.
echo "[D] sem canônico: precisa AVISAR, não mentir"
D="$TMP/d"; mk_repo "$D"
git -C "$D" checkout -q -b feat/sozinha
git -C "$D" branch -D "$CANON" >/dev/null 2>&1
out="$(add_inv "$D" "invariante do caso D")"
echo "$out" | grep -q "NÃO registrado no canônico" \
  && ok "avisa que não registrou no canônico" \
  || no "mentiu ou ficou mudo quando não deu pra gravar: $out"
echo "$out" | grep -q "commitado no canônico" \
  && no "CRÍTICO: anunciou sucesso sem ter gravado (o bug original)" \
  || ok "não anuncia sucesso falso"
grep -q "invariante do caso D" "$D/.claude/coord/invariants.txt" \
  && ok "mesmo falhando, o arquivo em disco fica com o invariante (gate segue protegendo)" \
  || no "perdeu o invariante do arquivo"

echo
echo "RESULTADO: $pass OK / $fail FALHAS"
[ "$fail" -eq 0 ]
