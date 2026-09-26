#!/usr/bin/env bash
# test-canon-guard.sh: the canonical branch never moves silently.
#
# Advancing the canonical branch has the widest reach in the project: from then on
# EVERY deploy of EVERY session includes what was promoted, and `git fetch .
# <branch>:refactor/foundation` does it without printing a line. Checking afterwards
# is too late, and raw git bypasses agentctl, so the barrier is a
# reference-transaction hook that runs BEFORE the move.
#
# The grep patterns below match the literal output of the hook and of agentctl,
# which live outside this repository.
#
# Usage: scripts/test-canon-guard.sh [path-to-agentctl]
set -uo pipefail

# Git environment isolation (do not remove): under pre-push git exports GIT_DIR and
# friends, which win even over `git -C`; without clearing them the fixtures would
# write into the REAL repository.
for _v in $(env | sed -n 's/^\(GIT_[A-Za-z0-9_]*\)=.*/\1/p'); do unset "$_v"; done
unset _v
# Inheriting the parent's sanction would make the blocking test pass by mistake.
unset AGENTCTL_CANON ALLOW_RAW_CANON 2>/dev/null || true

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
AGENTCTL="${1:-$ROOT/.claude/agentctl}"
HOOK="$ROOT/scripts/hooks/reference-transaction.sh"
[ -f "$AGENTCTL" ] || { echo "agentctl not found at $AGENTCTL"; exit 2; }
[ -f "$HOOK" ] || { echo "hook not found at $HOOK"; exit 2; }

pass=0; fail=0
ok() { echo "  ✓ $1"; pass=$((pass+1)); }
no() { echo "  ✗ $1"; fail=$((fail+1)); }
echo "=== test-canon-guard ==="

TMP="$(mktemp -d "${TMPDIR:-/tmp}/vpsm-guard.XXXXXX")" || exit 2
trap 'case "$TMP" in "${TMPDIR:-/tmp}"/vpsm-guard.*) rm -rf "$TMP";; esac' EXIT

CANON="refactor/foundation"

mk_repo() { # repo with the canonical branch, the hook installed and a work branch
  local d="$1" n="${2:-1}" i
  mkdir -p "$d/.claude/coord"
  git -C "$d" init -q -b "$CANON"
  git -C "$d" config user.email t@t; git -C "$d" config user.name t
  git -C "$d" config commit.gpgsign false
  echo base > "$d/f.txt"; git -C "$d" add -A >/dev/null 2>&1; git -C "$d" commit -q -m base
  mkdir -p "$d/.git/hooks"; ln -sf "$HOOK" "$d/.git/hooks/reference-transaction"
  git -C "$d" checkout -q -b feat/work
  for i in $(seq 1 "$n"); do
    echo "line $i" >> "$d/f.txt"; git -C "$d" add -A >/dev/null 2>&1
    git -C "$d" commit -q -m "work $i"
  done
  local top; top="$(git -C "$d" rev-parse --show-toplevel 2>/dev/null || true)"
  [ "$top" = "$(cd "$d" && pwd -P)" ] || { echo "  ✗ ABORTED: the fixture is not the target repo (GIT_DIR leaking?)"; exit 3; }
}
canon_at() { git -C "$1" rev-parse "$CANON"; }
sync_cmd() { local d="$1"; shift; VPSM_ROOT="$d" VPSM_CANON="$CANON" bash "$AGENTCTL" canon-sync "$@" 2>&1; }

# 1. Raw git is BLOCKED, and the barrier states the reach.
echo "[1] raw move of the canonical branch"
A="$TMP/a"; mk_repo "$A" 3
before="$(canon_at "$A")"
output="$(git -C "$A" fetch . "feat/work:$CANON" 2>&1)"
[ "$(canon_at "$A")" = "$before" ] \
  && ok "raw git fetch did NOT move the canonical branch" \
  || no "CRITICAL: raw git moved the canonical branch, the guard is not active"
echo "$output" | grep -q "BLOQUEADO" && ok "the refusal is explicit" || no "refused without explaining: $output"
echo "$output" | grep -q "promoveria 3 commit" \
  && ok "the refusal SAYS HOW MANY commits would be promoted" \
  || no "the refusal did not report the size of the delta"
echo "$output" | grep -q "canon-sync" && ok "points to the sanctioned path" || no "does not point to the alternative"

# Deleting the canonical branch does not pass either.
git -C "$A" checkout -q feat/work
output="$(git -C "$A" branch -D "$CANON" 2>&1)"
git -C "$A" rev-parse --verify --quiet "$CANON" >/dev/null \
  && ok "deleting the canonical branch is blocked" \
  || no "CRITICAL: a raw command DELETED the canonical branch"

# 2. The sanctioned path works.
echo "[2] agentctl canon-sync"
B="$TMP/b"; mk_repo "$B" 2
tip="$(git -C "$B" rev-parse feat/work)"
output="$(sync_cmd "$B" feat/work)"
git -C "$B" merge-base --is-ancestor "$tip" "$CANON" \
  && ok "canon-sync advances the canonical branch" \
  || no "canon-sync did not advance: $output"
echo "$output" | grep -q "promove 2 commit" \
  && ok "lists the delta BEFORE acting" \
  || no "did not show the delta"
echo "$output" | grep -q "✓ $CANON" && ok "confirms the resulting state" || no "did not confirm the resulting state"

# 3. A large delta requires confirmation.
echo "[3] large delta"
C="$TMP/c"; mk_repo "$C" 20
before="$(canon_at "$C")"
output="$(sync_cmd "$C" feat/work)"
[ "$(canon_at "$C")" = "$before" ] \
  && ok "a 20-commit delta is NOT promoted without confirmation" \
  || no "CRITICAL: promoted 20 commits without asking"
echo "$output" | grep -q -- "--yes" && ok "says how to confirm" || no "does not explain how to proceed"
output="$(sync_cmd "$C" feat/work --yes)"
git -C "$C" merge-base --is-ancestor "$(git -C "$C" rev-parse feat/work)" "$CANON" \
  && ok "with --yes, it promotes" \
  || no "--yes did not work: $output"

# 4. Non-fast-forward is refused: it would rewrite someone else's work.
echo "[4] non-fast-forward"
D="$TMP/d"; mk_repo "$D" 1
git -C "$D" checkout -q "$CANON"
echo divergent >> "$D/f.txt"; git -C "$D" add -A >/dev/null 2>&1
AGENTCTL_CANON=1 git -C "$D" commit -q -m "commit only on the canonical branch"
before="$(canon_at "$D")"
output="$(sync_cmd "$D" feat/work)"
[ "$(canon_at "$D")" = "$before" ] \
  && ok "refuses a non-fast-forward advance (does not rewrite others' work)" \
  || no "CRITICAL: rewrote the canonical branch"

# 5. The guard is surgical: other branches stay free.
echo "[5] scope"
E="$TMP/e"; mk_repo "$E" 1
git -C "$E" branch other "$CANON" 2>/dev/null
git -C "$E" fetch . feat/work:other >/dev/null 2>&1
[ "$(git -C "$E" rev-parse other)" = "$(git -C "$E" rev-parse feat/work)" ] \
  && ok "branches other than the canonical one stay free" \
  || no "the guard is blocking an ordinary branch"
ALLOW_RAW_CANON=1 git -C "$E" fetch . feat/work:"$CANON" >/dev/null 2>&1
git -C "$E" merge-base --is-ancestor "$(git -C "$E" rev-parse feat/work)" "$CANON" \
  && ok "ALLOW_RAW_CANON=1 allows the deliberate bypass" \
  || no "the emergency escape does not work"

echo
echo "RESULT: $pass OK / $fail FAILED"
[ "$fail" -eq 0 ]
