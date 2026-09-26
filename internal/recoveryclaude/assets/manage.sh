#!/usr/bin/env bash
# recovery-claude.sh: the Claude of the recovery screen.
#
# A dedicated container running ALONGSIDE the panel with an independent
# connection: no claude-router in the path and its own login, so it survives
# the situations /recovery exists for (router down, bad deploy live, broken
# Claude install on the host).
#
# Usage:
#   recovery-claude.sh build     # build the image
#   recovery-claude.sh up        # create/start the container (idempotent)
#   recovery-claude.sh down      # stop and remove the container (the login STAYS)
#   recovery-claude.sh status    # state, version and whether it has a login
#   recovery-claude.sh shell     # enter the dtach session (same path as the screen)
#   recovery-claude.sh doctor    # check the guarantees (no router, login, reach)
set -euo pipefail

IMAGE="vpsm-recovery-claude:latest"
NAME="vpsm-recovery-claude"
VOLUME="vpsm-recovery-claude-config"
# The build context is this script's own directory (Dockerfile, entrypoint and
# banner are its siblings), both in the repository and where the binary
# materializes it (<DataDir>/recovery-claude/).
CTX="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

msg() { printf '%s\n' "$*"; }
fail() { printf '\033[31m%s\033[0m\n' "$*" >&2; }

build() {
  msg "== building $IMAGE (takes a few minutes: Claude is ~340 MB) =="
  docker build -t "$IMAGE" "$CTX"
}

up() {
  docker volume inspect "$VOLUME" >/dev/null 2>&1 || docker volume create "$VOLUME" >/dev/null
  if ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
    fail "image missing, run: $0 build"
    return 1
  fi
  # `container inspect`, not plain `docker inspect`: the image has the same
  # name as the container and plain inspect would match it.
  if docker container inspect "$NAME" >/dev/null 2>&1; then
    docker start "$NAME" >/dev/null
    msg "container already existed, started"
    return 0
  fi
  # The flags below give FULL reach on purpose: whoever reaches /recovery
  # already has a root shell on the host, so this does not widen the surface.
  # --restart always makes the container independent of the panel (Docker
  # starts it at boot). There is deliberately no ANTHROPIC_BASE_URL here: its
  # absence is what keeps this Claude off the claude-router.
  docker run -d \
    --name "$NAME" \
    --restart always \
    --privileged \
    --pid host \
    --network host \
    --ipc host \
    -v "$VOLUME:/config" \
    -v /:/host \
    -v /opt/panel:/opt/panel \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -e CLAUDE_CONFIG_DIR=/config \
    -e VPSM_RECOVERY=1 \
    "$IMAGE" >/dev/null
  msg "container $NAME created and running"
}

down() {
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  msg "container removed (the $VOLUME volume, with the login, stays)"
}

status() {
  if ! docker container inspect "$NAME" >/dev/null 2>&1; then
    msg "state:    missing (run: $0 up)"
    return 0
  fi
  local state
  state="$(docker container inspect -f '{{.State.Status}}' "$NAME")"
  msg "state:    $state"
  msg "restart:  $(docker container inspect -f '{{.HostConfig.RestartPolicy.Name}}' "$NAME")"
  if [ "$state" = "running" ]; then
    msg "claude:   $(docker exec "$NAME" claude --version 2>/dev/null || echo unavailable)"
    if docker exec "$NAME" test -f /config/.credentials.json 2>/dev/null; then
      msg "login:    present (own credential)"
    else
      msg "login:    MISSING: open the Claude tab in /recovery and run 'claude' once"
    fi
  fi
}

doctor() {
  local failures=0
  check() { if [ "$2" = "ok" ]; then printf '  \033[32m✓\033[0m %s\n' "$1"; else printf '  \033[31m✗\033[0m %s\n' "$1"; failures=$((failures+1)); fi; }

  msg "== recovery Claude guarantees =="
  docker container inspect "$NAME" >/dev/null 2>&1 && check "container exists" ok || check "container exists" fail
  [ "$(docker container inspect -f '{{.State.Status}}' "$NAME" 2>/dev/null)" = "running" ] \
    && check "is running" ok || check "is running" fail
  [ "$(docker container inspect -f '{{.HostConfig.RestartPolicy.Name}}' "$NAME" 2>/dev/null)" = "always" ] \
    && check "starts on its own at boot (restart=always, independent of the panel)" ok \
    || check "starts on its own at boot" fail
  # The central guarantee: no variable pointing Claude at the router.
  if docker container inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$NAME" 2>/dev/null | grep -q '^ANTHROPIC_BASE_URL='; then
    check "no ANTHROPIC_BASE_URL (off the claude-router)" fail
  else
    check "no ANTHROPIC_BASE_URL (off the claude-router)" ok
  fi
  if docker exec "$NAME" sh -c 'grep -q ANTHROPIC_BASE_URL /config/settings.json 2>/dev/null' 2>/dev/null; then
    check "the container settings.json does not point at the router either" fail
  else
    check "the container settings.json does not point at the router either" ok
  fi
  # The login is a MANUAL one-time step (device flow), so a missing login is
  # "not configured yet", not a broken guarantee. Exit code 2 = only the login
  # is missing, 1 = something regressed.
  local noLogin=0
  if docker exec "$NAME" test -f /config/.credentials.json 2>/dev/null; then
    check "own login (does not share a refresh token with the host)" ok
  else
    printf '  \033[33m•\033[0m %s\n' "own login: NOT authenticated yet (run 'claude' once in the /recovery tab)"
    noLogin=1
  fi
  docker exec "$NAME" test -d /host/etc 2>/dev/null \
    && check "sees the host filesystem at /host" ok || check "sees /host" fail
  docker exec "$NAME" sh -c 'command -v hostctl >/dev/null' 2>/dev/null \
    && check "hostctl available (host systemctl/journalctl)" ok || check "hostctl" fail
  docker exec "$NAME" sh -c 'command -v docker >/dev/null && docker ps >/dev/null 2>&1' 2>/dev/null \
    && check "talks to the host Docker" ok || check "talks to the host Docker" fail
  if [ "$failures" != "0" ]; then fail "$failures check(s) failed"; return 1; fi
  if [ "$noLogin" != "0" ]; then msg "structure is up; only the login is missing."; return 2; fi
  msg "all good."
  return 0
}

case "${1:-status}" in
  build)   build ;;
  up)      up ;;
  down)    down ;;
  restart) docker restart "$NAME" >/dev/null && msg "restarted" ;;
  status)  status ;;
  doctor)  doctor ;;
  shell)   docker exec -it "$NAME" dtach -A /tmp/recovery.sock -E -z bash -l ;;
  *) fail "usage: $0 {build|up|down|restart|status|doctor|shell}"; exit 2 ;;
esac
