#!/usr/bin/env bash
# test-invariant-canon.sh: regression test for `agentctl invariant add` recording
# the invariant on the WRONG branch while announcing success.
#
# `git -C "$ROOT" commit` acts on the directory's CHECKED-OUT branch, and $ROOT
# lives on a feature branch. The gate reads invariants.txt FROM DISK, so nothing
# regresses at once: the damage is deferred until the feature branch is dropped and
# the protection vanishes silently. Every assertion therefore asks the CANONICAL
# branch what it really contains.
#
# The grep patterns on agentctl's output match its literal messages (agentctl
# lives outside this repository).
#
# Usage: scripts/test-invariant-canon.sh [path-to-agentctl]
set -uo pipefail

# Git environment isolation (do not remove): under pre-push git exports GIT_DIR and
# friends, which override repository discovery even for `git -C`; without clearing
# them the fixture repos would re-initialize the REAL repository.
for _v in $(env | sed -n 's/^\(GIT_[A-Za-z0-9_]*\)=.*/\1/p'); do unset "$_v"; done
unset _v

AGENTCTL="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/.claude/agentctl}"
[ -f "$AGENTCTL" ] || { echo "agentctl not found at $AGENTCTL"; exit 2; }

pass=0; fail=0
ok() { echo "  ✓ $1"; pass=$((pass+1)); }
no() { echo "  ✗ $1"; fail=$((fail+1)); }
echo "=== test-invariant-canon ==="

TMP="$(mktemp -d "${TMPDIR:-/tmp}/panel-inv-test.XXXXXX")" || exit 2
trap 'case "$TMP" in /tmp/panel-inv-test.*|"${TMPDIR:-/tmp}"/panel-inv-test.*) rm -rf "$TMP";; esac' EXIT

CANON="refactor/foundation"

# Fixture repo: canonical branch with a tracked invariants.txt + a feature branch.
mk_repo() {
  local d="$1"
  mkdir -p "$d/.claude/coord"
  git -C "$d" init -q -b "$CANON"
  git -C "$d" config user.email t@t; git -C "$d" config user.name t
  git -C "$d" config commit.gpgsign false
  printf 'file.go|baseSymbol|1|999|base invariant|-\n' > "$d/.claude/coord/invariants.txt"
  # Mirrors the real .gitignore: agentctl creates these runtime files on start;
  # without this the fixture itself would look like a dirty tree.
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
  # Isolation leaked? Aborting beats writing into the wrong repo.
  local top; top="$(git -C "$d" rev-parse --show-toplevel 2>/dev/null || true)"
  [ "$top" = "$(cd "$d" && pwd -P)" ] || { echo "  ✗ ABORTED: the fixture is not the target repo (GIT_DIR leaking?)"; exit 3; }
}

add_inv() {  # add_inv <root> <description>
  PANEL_ROOT="$1" PANEL_CANON="$CANON" bash "$AGENTCTL" invariant add \
    "file.go" "newSymbol" 1 999 "$2" - 2>&1
}

in_canon() {  # is the line in the CANONICAL branch's invariants.txt?
  git -C "$1" show "$CANON:.claude/coord/invariants.txt" 2>/dev/null | grep -q "$2"
}

# A. $ROOT on a feature branch, canonical branch in no worktree (the host's real
# topology, and the one that produced the bug).
echo "[A] \$ROOT on a feature branch (the host's real topology)"
A="$TMP/a"; mk_repo "$A"
git -C "$A" checkout -q -b feat/any
out="$(add_inv "$A" "case A invariant")"

in_canon "$A" "case A invariant" \
  && ok "the invariant landed ON THE CANONICAL branch" \
  || no "the invariant did NOT reach the canonical branch (the bug is back)"

echo "$out" | grep -q "committed to the canonical" \
  && ok "announces success" || no "did not announce success: $out"

[ -z "$(git -C "$A" status --porcelain)" ] \
  && ok "\$ROOT's tree stays clean (a dirty tree sabotages convergence)" \
  || no "left \$ROOT's tree dirty: $(git -C "$A" status --porcelain | head -2)"

[ "$(git -C "$A" symbolic-ref --short HEAD)" = "feat/any" ] \
  && ok "did not switch \$ROOT's branch under the user" \
  || no "\$ROOT's branch changed"

# Committing in both places would create twin commits that diverge forever. Since
# $ROOT is an ancestor of the canonical branch, it must ADOPT the commit by FF.
git -C "$A" merge-base --is-ancestor HEAD "$CANON" \
  && ok "\$ROOT adopted the canonical commit by FF (no diverging branches)" \
  || no "\$ROOT diverged from the canonical branch (twin commits)"
[ "$(git -C "$A" rev-list --count HEAD)" = "2" ] \
  && ok "a single commit in total (the record was not duplicated)" \
  || no "produced $(git -C "$A" rev-list --count HEAD) commits, expected 2"

grep -q "case A invariant" "$A/.claude/coord/invariants.txt" \
  && ok "the file on disk (what the gate reads) has the invariant" \
  || no "the file on disk lacks the invariant, the gate would not protect it"

# B. $ROOT already on the canonical branch.
echo "[B] \$ROOT already on the canonical branch"
B="$TMP/b"; mk_repo "$B"
add_inv "$B" "case B invariant" >/dev/null
in_canon "$B" "case B invariant" \
  && ok "records on the canonical branch" || no "did not record on the canonical branch"
[ -z "$(git -C "$B" status --porcelain)" ] \
  && ok "clean tree" || no "dirty tree"
[ "$(git -C "$B" rev-list --count HEAD)" = "2" ] \
  && ok "a single commit (no duplicate when already on the canonical branch)" \
  || no "produced $(git -C "$B" rev-list --count HEAD) commits, expected 2"

# C. Canonical branch CHECKED OUT in another worktree: moving the ref under it
# would leave that session with a phantom reverse diff.
echo "[C] canonical branch checked out in another worktree"
C="$TMP/c"; mk_repo "$C"
git -C "$C" checkout -q -b feat/other
git -C "$C" worktree add -q "$TMP/c-canon" "$CANON" 2>/dev/null
out="$(add_inv "$C" "case C invariant")"
in_canon "$C" "case C invariant" \
  && ok "records on the canonical branch even while it is checked out" || no "did not record on the canonical branch"
[ -z "$(git -C "$TMP/c-canon" status --porcelain)" ] \
  && ok "the canonical worktree has NO phantom diff" \
  || no "dirtied someone else's worktree: $(git -C "$TMP/c-canon" status --porcelain | head -2)"
[ -z "$(git -C "$C" status --porcelain)" ] \
  && ok "\$ROOT's tree clean" || no "\$ROOT's tree dirty"

# D. Honesty when recording is impossible: failing is fine, claiming success is not.
echo "[D] no canonical branch: must WARN, not lie"
D="$TMP/d"; mk_repo "$D"
git -C "$D" checkout -q -b feat/alone
git -C "$D" branch -D "$CANON" >/dev/null 2>&1
out="$(add_inv "$D" "case D invariant")"
echo "$out" | grep -q "NOT registered in the canonical" \
  && ok "warns it did not record on the canonical branch" \
  || no "lied or stayed silent when recording was impossible: $out"
echo "$out" | grep -q "committed to the canonical" \
  && no "CRITICAL: announced success without recording (the original bug)" \
  || ok "no false success"
grep -q "case D invariant" "$D/.claude/coord/invariants.txt" \
  && ok "even on failure the file on disk keeps the invariant (the gate still protects)" \
  || no "lost the invariant from the file"

echo
echo "RESULT: $pass OK / $fail FAILED"
[ "$fail" -eq 0 ]
