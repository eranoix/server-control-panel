#!/usr/bin/env bash
# test-canon-advance.sh — regressão do clobber SILENCIOSO do avanço do canônico
# Rápido, hermético e sem dependência de host: só `git` em repos
# descartáveis. Por isso pode rodar no pre-push e no CI, ao contrário da suíte
# test-agentmesh.sh (que precisa da toolchain Tailwind e do host canônico).
#
# O BUG: `_advance_canon_and_publish` rodava `git -C "$ROOT" merge --ff-only
# "$br"`. Mas `git merge` age na branch CHECKED-OUT do diretório — e o $ROOT
# (/opt/panel) vive numa branch de feature, não no canônico. O FF avançava
# a branch ERRADA. E como o `echo` seguinte lia o SHA do canônico depois do
# merge, imprimia "✓ refactor/foundation → <sha antigo>": sucesso reportado sem
# ter avançado nada.
#
# POR QUE IMPORTA: o binário ia pro ar com a correção e o canônico ficava sem
# ela. Como todo deploy auto-converge o canônico no build, o próximo deploy de
# QUALQUER sessão traria o canônico velho e reverteria o trabalho — exatamente o
# clobber que o agent-mesh existe para impedir, falhando em silêncio.
#
# POR QUE O TESTE É COMPORTAMENTAL: asserção estrutural não bastaria — um `grep`
# por `_advance_canon_and_publish()` passava alegremente com o bug ativo. Aqui a
# FUNÇÃO REAL é extraída do agentctl e exercitada contra a topologia real.
#
# Uso: scripts/test-canon-advance.sh [caminho-do-agentctl]
set -uo pipefail

# ─── ISOLAMENTO DO AMBIENTE GIT (não remova) ────────────────────────────────
# Rodando dentro de um hook (pre-push), o git EXPORTA GIT_DIR e companhia. Essas
# variáveis SOBREPÕEM a descoberta de repositório — inclusive `git -C <dir>`, que
# troca o cwd mas NÃO o GIT_DIR. Sem limpar isto, o `git init` do mk() responde
# "warning: re-init" e reinicializa o REPOSITÓRIO REAL, e todo `git -C` seguinte
# escreve nele. Estrago real em 2026-08-18 ao publicar a main: core.bare=true
# (quebra toda operação de working tree), user.name/email trocados para t@t
# (commits com autoria errada), branches de fixture e worktrees fantasma no repo
# de trabalho. O teste é inofensivo sozinho justamente porque aí não há GIT_DIR.
for _v in $(env | sed -n 's/^\(GIT_[A-Za-z0-9_]*\)=.*/\1/p'); do unset "$_v"; done
unset _v

# Aborta em vez de seguir escrevendo no lugar errado: se um repo de fixture não
# nasceu onde devia, o isolamento furou e continuar significa mexer no repo real.
_assert_isolado() {
  local dir="$1" top
  [ -d "$dir/.git" ] || {
    echo "  ✗ ABORTADO: '$dir' não virou repositório — isolamento furado (GIT_DIR no ambiente?)"; exit 3; }
  top="$(git -C "$dir" rev-parse --show-toplevel 2>/dev/null || true)"
  [ "$top" = "$(cd "$dir" && pwd -P)" ] || {
    echo "  ✗ ABORTADO: '$dir' resolve para '$top' — os comandos iriam para o repo errado"; exit 3; }
}

AGENTCTL="${1:-${AGENTCTL:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/.claude/agentctl}}"
CANON="refactor/foundation"
pass=0; fail=0
ok() { echo "  ✓ $1"; pass=$((pass + 1)); }
no() { echo "  ✗ $1"; fail=$((fail + 1)); }

echo "═══ test-canon-advance ═══"
[ -f "$AGENTCTL" ] || { echo "  ✗ agentctl não encontrado em $AGENTCTL"; exit 1; }

# ─── (a) estrutural: o padrão bugado não pode voltar ────────────────────────
# Sozinhas essas asserções não protegem (ver cabeçalho), mas pegam a regressão
# no ponto exato em que ela seria reintroduzida por um refactor descuidado.
# O que é proibido não é o `merge` em si — é usá-lo esperando que ele mova o
# CANÔNICO. `git merge` move sempre a branch checked-out, então `merge --ff-only
# <branch-de-ticket>` dentro do $ROOT avança a branch do $ROOT: era o bug.
# O sentido inverso, `merge --ff-only "$CANON"`, é legítimo e não tem como ser
# confundido — ali a intenção É mover a branch do $ROOT até o canônico (o
# agentctl usa isso pra adotar a commit de invariante sem criar uma gêmea).
# Por isso a regra olha o ARGUMENTO, não só o comando.
if grep -E 'git -C "\$ROOT" merge --ff-only' "$AGENTCTL" | grep -qv '"\$CANON"'; then
  no "voltou o 'git -C \$ROOT merge --ff-only <outra-branch>' (avança a branch do \$ROOT, não o canônico)"
else
  ok "sem 'git -C \$ROOT merge --ff-only' (o padrão que causava o clobber)"
fi
grep -q '_ff_canon_to()' "$AGENTCTL" \
  && ok "helper _ff_canon_to presente" || no "helper _ff_canon_to ausente"
grep -q 'merge-base --is-ancestor "\$br_tip" "\$CANON"' "$AGENTCTL" \
  && ok "avanço é VERIFICADO antes de imprimir ✓" \
  || no "avanço não é verificado (foi assim que o bug passou despercebido)"

# ─── (b) comportamental: exercita a função REAL ─────────────────────────────
# _ff_canon_to delega a descoberta do worktree do canônico a _canon_worktree_dir
# (extraído depois, quando o helper passou a ser compartilhado com o
# registro de invariantes). Extrair só a primeira deixaria a rota 2 chamando uma
# função inexistente — o teste falharia por artefato da extração, não por bug.
fn="$(sed -n '/^_ff_canon_to()/,/^}/p' "$AGENTCTL")"
helper="$(sed -n '/^_canon_worktree_dir()/,/^}/p' "$AGENTCTL")"
if [ -z "$fn" ]; then
  no "não consegui extrair _ff_canon_to do agentctl"
else
  [ -n "$helper" ] && eval "$helper"
  eval "$fn"
  g() { git -C "$1" "${@:2}"; }

  # Monta a topologia REAL: $ROOT numa branch de feature (não no canônico) e o
  # trabalho do ticket num worktree à frente.
  mk() {
    # O `rm -rf` abaixo só pode agir dentro do tmpdir desta execução.
    [ -n "${1:-}" ] || { echo "  ✗ ABORTADO: mk() sem destino"; exit 3; }
    case "$1" in "$T"/*) ;; *) echo "  ✗ ABORTADO: mk() fora do tmpdir ('$1')"; exit 3;; esac
    rm -rf "$1"; mkdir -p "$1/root"
    git init -q -b "$CANON" "$1/root"
    _assert_isolado "$1/root"
    g "$1/root" config user.email t@t; g "$1/root" config user.name t
    echo base > "$1/root/f"; g "$1/root" add f; g "$1/root" commit -qm base
    g "$1/root" checkout -q -b feat/x
    g "$1/root" worktree add -q "$1/wt" -b ticket "$CANON"
    echo novo > "$1/wt/f"; g "$1/wt" commit -qam "trabalho do ticket"
  }

  T="$(mktemp -d)"
  [ -n "$T" ] && [ -d "$T" ] || { echo "  ✗ ABORTADO: mktemp -d falhou"; exit 3; }
  trap 'rm -rf "$T"' EXIT

  # Cenário 1 — o bug original: canônico não está checked-out em lugar nenhum.
  mk "$T/c1"; ROOT="$T/c1/root"
  tip="$(g "$T/c1/wt" rev-parse HEAD)"; feat_before="$(g "$ROOT" rev-parse feat/x)"
  _ff_canon_to ticket
  g "$ROOT" merge-base --is-ancestor "$tip" "$CANON" \
    && ok "canônico avançou até o tip do ticket" \
    || no "canônico NÃO avançou (o bug voltou)"
  [ "$(g "$ROOT" rev-parse feat/x)" = "$feat_before" ] \
    && ok "a branch do \$ROOT ficou intacta" \
    || no "moveu a branch do \$ROOT — assinatura EXATA do bug"

  # Cenário 2 — canônico ESTÁ checked-out num worktree (rota 2 da função, onde
  # o refspec é recusado e o FF tem de acontecer dentro daquele worktree).
  mk "$T/c2"; ROOT="$T/c2/root"
  g "$ROOT" worktree add -q "$T/c2/canon" "$CANON"
  tip="$(g "$T/c2/wt" rev-parse HEAD)"
  _ff_canon_to ticket
  g "$ROOT" merge-base --is-ancestor "$tip" "$CANON" \
    && ok "avança mesmo com o canônico checked-out (rota 2)" \
    || no "falhou com o canônico checked-out"

  # Cenário 3 — segurança: história divergente NÃO pode reescrever o canônico.
  # Um "FF" falso aqui apagaria trabalho alheio já publicado.
  mk "$T/c3"; ROOT="$T/c3/root"
  g "$ROOT" checkout -q "$CANON"; echo divergente > "$ROOT/f"
  g "$ROOT" commit -qam "commit só no canônico"; g "$ROOT" checkout -q feat/x
  canon_before="$(g "$ROOT" rev-parse "$CANON")"
  if _ff_canon_to ticket; then
    no "aceitou avanço NÃO-fast-forward (reescreveria o canônico)"
  else
    ok "recusa avanço não-fast-forward"
  fi
  [ "$(g "$ROOT" rev-parse "$CANON")" = "$canon_before" ] \
    && ok "canônico intacto após a recusa" \
    || no "canônico foi alterado apesar da recusa"
fi

echo "─────────────────────────────────────"
echo "RESULTADO: $pass OK / $fail FALHAS"
[ "$fail" -eq 0 ]
