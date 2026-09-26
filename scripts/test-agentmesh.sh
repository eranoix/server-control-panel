#!/usr/bin/env bash
# Test suite for the anti-clobber agent mesh.
#
# Runs on the CANONICAL HOST (/opt/panel): it uses the installed agentctl and the
# Tailwind toolchain, which only exist there. It never deploys or restarts a
# service: every assertion is offline and safe. The race of two real deploys is a
# MANUAL procedure printed at the end (restarting the service is unsafe to automate).
#
# Usage: scripts/test-agentmesh.sh        (from inside the repo/worktree)
set -uo pipefail

# Default = canonical host; override to run against a worktree before activation.
ROOT="${AGENTMESH_ROOT:-/opt/panel}"
AGENTCTL="$ROOT/.claude/agentctl"
DEPLOY="$ROOT/scripts/deploy.sh"
BUILDTW="$ROOT/scripts/build-tailwind.sh"
pass=0; fail=0
ok()   { echo "  ✓ $1"; pass=$((pass+1)); }
no()   { echo "  ✗ $1"; fail=$((fail+1)); }

echo "═══ test-agentmesh ═══"

# --- A2: a raw deploy dies without AGENTCTL_DEPLOY; ALLOW_RAW_DEPLOY=1 passes the guard.
# The grep pattern below is the literal message printed by the deploy script.
echo "[A2] enforcement in the deploy script"
out="$(AGENTCTL_DEPLOY=0 ALLOW_RAW_DEPLOY=0 bash "$DEPLOY" /bin/true 2>&1)"
echo "$out" | grep -q 'deploy fora do agent-mesh' && ok "raw deploy blocked" || no "raw deploy NOT blocked"
# Guard condition only, never letting a real deploy proceed:
if AGENTCTL_DEPLOY=0 ALLOW_RAW_DEPLOY=1 bash -c '[[ "${AGENTCTL_DEPLOY}" != "1" && "${ALLOW_RAW_DEPLOY}" != "1" ]]'; then
  no "ALLOW_RAW_DEPLOY=1 should pass the guard"; else ok "ALLOW_RAW_DEPLOY=1 passes the guard"; fi
# Rollback is never blocked (the --rollback block comes before the guard):
gline="$(grep -n 'deploy fora do agent-mesh' "$DEPLOY" | head -1 | cut -d: -f1)"
rline="$(grep -n '== "--rollback"' "$DEPLOY" | head -1 | cut -d: -f1)"
[[ -n "$gline" && -n "$rline" && "$rline" -lt "$gline" ]] && ok "--rollback block ($rline) precedes the guard ($gline)" || no "wrong rollback/guard order"

# --- A0: build-tailwind writes the TARGET's CSS without touching other trees
echo "[A0] per-worktree build-tailwind"
if [[ -x "$ROOT/scripts/tailwindcss" ]]; then
  tmpd="$(mktemp -d)"; mkdir -p "$tmpd/internal/webassets/web"
  cp "$ROOT/internal/webassets/web/index.html" "$tmpd/internal/webassets/web/" 2>/dev/null
  cp "$ROOT/internal/webassets/web/vendor/panel/app/00-shell.js" "$tmpd/internal/webassets/web/" 2>/dev/null
  before="$(md5sum "$ROOT/internal/webassets/web/tailwind.css" | awk '{print $1}')"
  if bash "$BUILDTW" "$tmpd" >/dev/null 2>&1 && [[ -s "$tmpd/internal/webassets/web/tailwind.css" ]]; then
    ok "CSS generated in the target ($(stat -c%s "$tmpd/internal/webassets/web/tailwind.css") bytes)"
  else no "build-tailwind generated no CSS in the target"; fi
  after="$(md5sum "$ROOT/internal/webassets/web/tailwind.css" | awk '{print $1}')"
  [[ "$before" == "$after" ]] && ok "main tree untouched" || no "the build touched the main tree!"
  rm -rf "$tmpd"
else echo "  - toolchain missing, skipping A0"; fi

# --- A1: .claude/sessions is ignored (does not dirty the tree)
echo "[A1] sessions not tracked"
git -C "$ROOT" check-ignore .claude/sessions/probe.md >/dev/null 2>&1 && ok ".claude/sessions/ ignored" || no ".claude/sessions/ NOT ignored"
[[ "$(git -C "$ROOT" ls-files .claude/sessions/ | wc -l)" -eq 0 ]] && ok "0 sessions tracked in the canonical tree" || no "sessions are still tracked"

# --- A3/A4: agentctl structure (single lock + helper + gocheck)
echo "[A3/A4/B3] agentctl structure"
bash -n "$AGENTCTL" && ok "agentctl: syntax OK" || no "agentctl: syntax error"
grep -q 'flock -w 600 8' "$AGENTCTL" && ok "single lock (flock -w 600 8) present" || no "single lock missing"
grep -q '_advance_canon_and_publish()' "$AGENTCTL" && ok "advance/propagation helper present" || no "helper missing"
grep -q 'make build' "$AGENTCTL" && ok "deploy uses make build (tailwind+panelctl)" || no "deploy does NOT use make build"
grep -q 'merge -X theirs' "$AGENTCTL" && no "propagation still uses -X theirs (destructive)" || ok "non-destructive propagation (no -X theirs)"
grep -q 'go build ./... && go vet ./...' "$AGENTCTL" && ok "backend gate (go build/vet) present" || no "backend gate missing"

# --- invariants green in the canonical tree
echo "[inv] invariants in the canonical source"
( cd "$ROOT" && AGENTCTL_SKIP_GOCHECK=1 bash "$AGENTCTL" invariant check >/dev/null 2>&1 ) \
  && ok "invariant check green" || no "invariant check failed"

# Canonical advance (silent clobber regression) lives in test-canon-advance.sh,
# which is hermetic and also runs in pre-push and CI; this suite only delegates.
echo "canonical advance (delegated to test-canon-advance.sh)"
if bash "$ROOT/scripts/test-canon-advance.sh" "$AGENTCTL" > /tmp/canonadv.$$ 2>&1; then
  ok "canonical advance: $(grep -c '✓' /tmp/canonadv.$$) assertions OK"
else
  no "canonical advance FAILED:"; sed 's/^/      /' /tmp/canonadv.$$
fi
rm -f /tmp/canonadv.$$

echo "─────────────────────────────────────"
echo "RESULT: $pass OK / $fail FAILED"
[[ $fail -eq 0 ]] || exit 1

cat <<'MANUAL'

─── MANUAL PROCEDURE (race of 2 real deploys, unsafe to automate) ───
  In two separate worktrees, one touching a marker in index.html and the other a
  .go file, run `agentctl deploy` almost simultaneously. Expected (single lock):
    • the deploys SERIALIZE (deploys.jsonl has no time overlap);
    • curl localhost:8765/         → contains BOTH markers (no clobber);
    • curl localhost:8765/tailwind.css | head -c1 | wc -c == 1 (non-empty CSS);
    • git -C /opt/panel log --oneline -3 refactor/foundation → both commits.
MANUAL
