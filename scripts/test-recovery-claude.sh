#!/usr/bin/env bash
# test-recovery-claude.sh: the recovery screen's Claude must stay INDEPENDENT, and
# independence is easy to lose unnoticed (an ANTHROPIC_BASE_URL added "to
# standardize", or the host's .credentials.json mounted "to avoid logging in
# twice"), so the guarantees are asserted here.
#
# Static checks run anywhere (CI included). Runtime checks only run where the
# container exists; its absence does NOT fail, because CI has no host Docker.
#
# NOTE: several checks grep manage.sh, Dockerfile, entrypoint.sh and
# handlers_recovery.go for literal code (variable names, flags); keep them in sync.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

pass=0; fail=0
ok()  { printf 'PASS %s\n' "$1"; pass=$((pass+1)); }
no()  { printf 'FAIL %s\n' "$1"; fail=$((fail+1)); }
skip(){ printf 'SKIP %s\n' "$1"; }

echo "=== test-recovery-claude ==="

DOCKERFILE="internal/recoveryclaude/assets/Dockerfile"
SCRIPT="internal/recoveryclaude/assets/manage.sh"
ENTRY="internal/recoveryclaude/assets/entrypoint.sh"
HANDLER="internal/api/handlers_recovery.go"

for f in "$DOCKERFILE" "$SCRIPT" "$ENTRY" "$HANDLER"; do
  [ -f "$f" ] || { no "missing file: $f"; }
done

# 1. The core guarantee: nothing points this Claude at a custom API base URL.
if grep -q 'ANTHROPIC_BASE_URL' "$DOCKERFILE" "$ENTRY" 2>/dev/null | grep -qv '^\s*#'; then
  no "ANTHROPIC_BASE_URL appears in the image: the container no longer talks straight to the API"
else
  # Comments explaining the absence are fine; ENV/export is not.
  if grep -E '^(ENV|export)[[:space:]]+ANTHROPIC_BASE_URL' "$DOCKERFILE" "$ENTRY" >/dev/null 2>&1; then
    no "ANTHROPIC_BASE_URL set in the image: independence is gone"
  else
    ok "image does not set ANTHROPIC_BASE_URL (direct API connection)"
  fi
fi
if grep -E '(^|[[:space:]])-e[[:space:]]+ANTHROPIC_BASE_URL' "$SCRIPT" >/dev/null 2>&1; then
  no "the start script injects ANTHROPIC_BASE_URL"
else
  ok "the start script does not inject ANTHROPIC_BASE_URL"
fi

# 2. Its OWN login: borrowing the host credential would make both sides refresh the
# same token and invalidate each other, so the emergency tool could take down the
# main Claude.
if grep -E '/root/\.claude|\.credentials\.json' "$SCRIPT" | grep -E '^\s*[^#]*-v ' >/dev/null 2>&1; then
  no "the container mounts the host credential: the two refresh tokens invalidate each other"
else
  ok "does not mount the host credential (own login)"
fi
if grep -q 'VOLUME \["/config"\]' "$DOCKERFILE" && grep -q '\$VOLUME:/config' "$SCRIPT"; then
  ok "config dir is a named volume (the login survives rebuild and docker rm)"
else
  no "config dir is not a volume: the login would be lost on the first rebuild"
fi

# 3. Independent from the panel: Docker starts it at boot.
if grep -q -- '--restart always' "$SCRIPT"; then
  ok "restart always (starts at boot without depending on anything of ours)"
else
  no "no restart always: the container would not come back on its own"
fi

# 4. The entry point stays gated by the recovery auth.
if grep -A 6 'func (r \*Router) handleRecoveryClaudePTY' "$HANDLER" | grep -q 'recoveryUserFromCookie'; then
  ok "the Claude terminal requires the recovery session"
else
  no "handleRecoveryClaudePTY without a session check: open route"
fi
if grep -A 6 'func (r \*Router) handleRecoveryClaudeStatus' "$HANDLER" | grep -q 'recoveryUserFromCookie'; then
  ok "the container status requires the recovery session"
else
  no "handleRecoveryClaudeStatus without a session check"
fi
if grep -q '/recovery/ws/claude' internal/api/api.go && grep -q '/recovery/claude/status' internal/api/api.go; then
  ok "routes registered"
else
  no "the recovery Claude routes are not registered"
fi

# 5. `container inspect`, never `inspect`: the image has the SAME name as the
# container, and `docker inspect` matches both.
if grep -nE '^[^#]*docker (container )?inspect' "$SCRIPT" | grep -vq 'container inspect'; then
  no "the script uses 'docker inspect' (also matches the image) instead of 'container inspect'"
else
  ok "script uses 'docker container inspect' (no ambiguity with the image)"
fi
if grep -A 2 '"/usr/bin/docker"' "$HANDLER" | grep -q '"inspect"' && ! grep -A 1 '"/usr/bin/docker"' "$HANDLER" | grep -q '"container", "inspect"'; then
  no "the handler uses 'docker inspect' instead of 'container inspect'"
else
  ok "handler uses 'docker container inspect'"
fi

# 6. The binary must carry what it needs. A script at a fixed path fails because
# the deploy builds from the ticket's worktree while /opt/panel is on another
# branch, and a deploy step that installs the script never runs from a worktree.
if grep -qE '"/opt/panel/scripts/|/usr/local/bin/panel-recovery-claude' "$HANDLER"; then
  no "the handler depends on a script at a fixed disk path again"
else
  ok "handler does not depend on a script at a fixed path (neither repo nor /usr/local/bin)"
fi
if grep -q 'recoveryclaude.Command' "$HANDLER"; then
  ok "handler materializes the manager EMBEDDED in the binary (travels with the deploy)"
else
  no "handler does not use the embedded manager: it depends on the disk again"
fi
if grep -q 'go:embed assets' internal/recoveryclaude/recoveryclaude.go; then
  ok "Dockerfile and scripts are embedded in the binary"
else
  no "assets are not embedded: the binary does not carry what it needs"
fi
# The build context must be the script's own directory, so the same file works in
# the repo AND materialized under <DataDir>.
if grep -q 'CTX="$(cd "$(dirname "${BASH_SOURCE\[0\]}")" && pwd)"' "$SCRIPT"; then
  ok "build context is the script's own directory (works in both places)"
else
  no "build context points elsewhere: breaks when materialized"
fi

# 7. Named session: the conversation survives a reconnect ("attach if it exists,
# create if not").
if grep -q 'dtach -A /tmp/recovery.sock' "$HANDLER"; then
  ok "attaches to a named dtach session (reconnecting keeps the conversation)"
else
  no "no named session: every reconnect would start from scratch"
fi

# 8. Runtime (only where the container exists).
if ! command -v docker >/dev/null 2>&1; then
  skip "runtime checks: docker missing in this environment"
elif ! docker inspect panel-recovery-claude >/dev/null 2>&1; then
  skip "runtime checks: container not created yet (scripts/recovery-claude.sh up)"
else
  bash "$SCRIPT" doctor >/dev/null 2>&1
  case $? in
    0) ok "container doctor passed (no base URL, own login, host reach)" ;;
    # 2 = structure up, only the one-time manual login is missing; that is a
    # setup step, not a regression.
    2) skip "doctor: structure up; the manual login is missing (run 'claude' in the /recovery tab)" ;;
    *) no "container doctor failed: run $SCRIPT doctor" ;;
  esac
fi

echo "─────────────────────────────────────"
echo "RESULT: $pass OK / $fail FAILED"
[ "$fail" = "0" ]
