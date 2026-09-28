#!/usr/bin/env bash
set -euo pipefail

IMAGE="panel-recovery-claude:latest"
NAME="panel-recovery-claude"
VOLUME="panel-recovery-claude-config"
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
  if docker container inspect "$NAME" >/dev/null 2>&1; then
    docker start "$NAME" >/dev/null
    msg "container already existed, started"
    return 0
  fi
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
    -e PANEL_RECOVERY=1 \
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
  if docker container inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$NAME" 2>/dev/null | grep -q '^ANTHROPIC_BASE_URL='; then
    check "no ANTHROPIC_BASE_URL (direct API connection)" fail
  else
    check "no ANTHROPIC_BASE_URL (direct API connection)" ok
  fi
  if docker exec "$NAME" sh -c 'grep -q ANTHROPIC_BASE_URL /config/settings.json 2>/dev/null' 2>/dev/null; then
    check "the container settings.json does not set a base URL either" fail
  else
    check "the container settings.json does not set a base URL either" ok
  fi
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
