#!/usr/bin/env bash
# test-canon-advance.sh: regression test for the SILENT clobber of the canonical
# branch advance. Fast, hermetic and host-independent (only `git` in throwaway
# repos), so it can run in pre-push and CI, unlike test-agentmesh.sh.
#
# The bug: `_advance_canon_and_publish` ran `git -C "$ROOT" merge --ff-only "$br"`,
# but `git merge` moves the CHECKED-OUT branch of that directory, and $ROOT lives on
# a feature branch. The wrong branch advanced while success was reported, so the
# next deploy from any session would converge on the stale canonical branch and
# revert the work.
#
# The test is behavioural because a structural grep passed with the bug active:
# the REAL function is extracted from agentctl and exercised on the real topology.
#
# Usage: scripts/test-canon-advance.sh [path-to-agentctl]
set -uo pipefail

# Git environment isolation (do not remove). Inside a hook (pre-push) git EXPORTS
# GIT_DIR and friends, which override repository discovery even for `git -C <dir>`.
# Without clearing them, mk()'s `git init` re-initializes the REAL repository and
# every later `git -C` writes into it.
for _v in $(env | sed -n 's/^\(GIT_[A-Za-z0-9_]*\)=.*/\1/p'); do unset "$_v"; done
unset _v

# Abort instead of writing to the wrong place: a fixture repo that was not created
# where expected means the isolation leaked.
_assert_isolated() {
  local dir="$1" top
  [ -d "$dir/.git" ] || {
    echo "  ✗ ABORTED: '$dir' did not become a repository, isolation leaked (GIT_DIR in the environment?)"; exit 3; }
  top="$(git -C "$dir" rev-parse --show-toplevel 2>/dev/null || true)"
  [ "$top" = "$(cd "$dir" && pwd -P)" ] || {
    echo "  ✗ ABORTED: '$dir' resolves to '$top', commands would hit the wrong repo"; exit 3; }
}

AGENTCTL="${1:-${AGENTCTL:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/.claude/agentctl}}"
CANON="refactor/foundation"
pass=0; fail=0
ok() { echo "  ✓ $1"; pass=$((pass + 1)); }
no() { echo "  ✗ $1"; fail=$((fail + 1)); }

echo "═══ test-canon-advance ═══"
[ -f "$AGENTCTL" ] || { echo "  ✗ agentctl not found at $AGENTCTL"; exit 1; }

# (a) Structural: the buggy pattern must not come back.
# What is forbidden is `merge --ff-only <ticket-branch>` inside $ROOT (it moves
# $ROOT's branch). The reverse, `merge --ff-only "$CANON"`, is legitimate (it moves
# $ROOT's branch up to the canonical one on purpose), so the rule looks at the
# ARGUMENT, not just the command.
if grep -E 'git -C "\$ROOT" merge --ff-only' "$AGENTCTL" | grep -qv '"\$CANON"'; then
  no "'git -C \$ROOT merge --ff-only <other-branch>' is back (advances \$ROOT's branch, not the canonical one)"
else
  ok "no 'git -C \$ROOT merge --ff-only' (the pattern that caused the clobber)"
fi
grep -q '_ff_canon_to()' "$AGENTCTL" \
  && ok "helper _ff_canon_to present" || no "helper _ff_canon_to missing"
grep -q 'merge-base --is-ancestor "\$br_tip" "\$CANON"' "$AGENTCTL" \
  && ok "the advance is VERIFIED before printing ✓" \
  || no "the advance is not verified"

# (b) Behavioural: exercises the REAL function.
# _ff_canon_to delegates finding the canonical worktree to _canon_worktree_dir, so
# both are extracted; otherwise route 2 would call a missing function.
fn="$(sed -n '/^_ff_canon_to()/,/^}/p' "$AGENTCTL")"
helper="$(sed -n '/^_canon_worktree_dir()/,/^}/p' "$AGENTCTL")"
if [ -z "$fn" ]; then
  no "could not extract _ff_canon_to from agentctl"
else
  [ -n "$helper" ] && eval "$helper"
  eval "$fn"
  g() { git -C "$1" "${@:2}"; }

  # Builds the REAL topology: $ROOT on a feature branch (not the canonical one)
  # and the ticket's work in a worktree ahead of it.
  mk() {
    # The `rm -rf` below may only act inside this run's tmpdir.
    [ -n "${1:-}" ] || { echo "  ✗ ABORTED: mk() without a destination"; exit 3; }
    case "$1" in "$T"/*) ;; *) echo "  ✗ ABORTED: mk() outside the tmpdir ('$1')"; exit 3;; esac
    rm -rf "$1"; mkdir -p "$1/root"
    git init -q -b "$CANON" "$1/root"
    _assert_isolated "$1/root"
    g "$1/root" config user.email t@t; g "$1/root" config user.name t
    echo base > "$1/root/f"; g "$1/root" add f; g "$1/root" commit -qm base
    g "$1/root" checkout -q -b feat/x
    g "$1/root" worktree add -q "$1/wt" -b ticket "$CANON"
    echo new > "$1/wt/f"; g "$1/wt" commit -qam "ticket work"
  }

  T="$(mktemp -d)"
  [ -n "$T" ] && [ -d "$T" ] || { echo "  ✗ ABORTED: mktemp -d failed"; exit 3; }
  trap 'rm -rf "$T"' EXIT

  # Scenario 1, the original bug: the canonical branch is not checked out anywhere.
  mk "$T/c1"; ROOT="$T/c1/root"
  tip="$(g "$T/c1/wt" rev-parse HEAD)"; feat_before="$(g "$ROOT" rev-parse feat/x)"
  _ff_canon_to ticket
  g "$ROOT" merge-base --is-ancestor "$tip" "$CANON" \
    && ok "canonical branch advanced to the ticket tip" \
    || no "canonical branch did NOT advance (the bug is back)"
  [ "$(g "$ROOT" rev-parse feat/x)" = "$feat_before" ] \
    && ok "\$ROOT's branch untouched" \
    || no "moved \$ROOT's branch, the EXACT signature of the bug"

  # Scenario 2: the canonical branch IS checked out in a worktree (route 2, where
  # the refspec is refused and the FF must happen inside that worktree).
  mk "$T/c2"; ROOT="$T/c2/root"
  g "$ROOT" worktree add -q "$T/c2/canon" "$CANON"
  tip="$(g "$T/c2/wt" rev-parse HEAD)"
  _ff_canon_to ticket
  g "$ROOT" merge-base --is-ancestor "$tip" "$CANON" \
    && ok "advances even with the canonical branch checked out (route 2)" \
    || no "failed with the canonical branch checked out"

  # Scenario 3, safety: divergent history must NOT rewrite the canonical branch
  # (a fake FF would erase published work).
  mk "$T/c3"; ROOT="$T/c3/root"
  g "$ROOT" checkout -q "$CANON"; echo divergent > "$ROOT/f"
  g "$ROOT" commit -qam "commit only on the canonical branch"; g "$ROOT" checkout -q feat/x
  canon_before="$(g "$ROOT" rev-parse "$CANON")"
  if _ff_canon_to ticket; then
    no "accepted a NON-fast-forward advance (would rewrite the canonical branch)"
  else
    ok "refuses a non-fast-forward advance"
  fi
  [ "$(g "$ROOT" rev-parse "$CANON")" = "$canon_before" ] \
    && ok "canonical branch intact after the refusal" \
    || no "canonical branch changed despite the refusal"
fi

echo "─────────────────────────────────────"
echo "RESULT: $pass OK / $fail FAILED"
[ "$fail" -eq 0 ]
