#!/usr/bin/env bash
# recovery-claude.sh — o Claude da tela de recuperacao.
#
# Um container dedicado, rodando EM PARALELO ao vps-manager, com conexao
# independente: sem claude-router no caminho e com login proprio. O ponto e
# sobreviver ao cenario em que se recorre ao /recovery — router fora, deploy
# ruim no ar, instalacao do Claude do host quebrada.
#
# Uso:
#   recovery-claude.sh build     # constroi a imagem
#   recovery-claude.sh up        # cria/inicia o container (idempotente)
#   recovery-claude.sh down      # para e remove o container (o login PERMANECE)
#   recovery-claude.sh status    # estado, versao e se ja tem login
#   recovery-claude.sh shell     # entra na sessao dtach (mesmo caminho da tela)
#   recovery-claude.sh doctor    # checa as garantias (sem router, login, alcance)
set -euo pipefail

IMAGEM="vpsm-recovery-claude:latest"
NOME="vpsm-recovery-claude"
VOLUME="vpsm-recovery-claude-config"
# O contexto de build e o PROPRIO diretorio deste script — Dockerfile,
# entrypoint e banner sao irmaos dele. Vale nos dois lugares onde ele roda: no
# repositorio (internal/recoveryclaude/assets/) e materializado pelo binario em
# <DataDir>/recovery-claude/. Sem caminho relativo pra fora, sem depender de
# qual branch o working tree esta.
CTX="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

msg() { printf '%s\n' "$*"; }
erro() { printf '\033[31m%s\033[0m\n' "$*" >&2; }

build() {
  msg "== construindo $IMAGEM (leva alguns minutos: o Claude tem ~340 MB) =="
  docker build -t "$IMAGEM" "$CTX"
}

up() {
  docker volume inspect "$VOLUME" >/dev/null 2>&1 || docker volume create "$VOLUME" >/dev/null
  if ! docker image inspect "$IMAGEM" >/dev/null 2>&1; then
    erro "imagem ausente — rode: $0 build"
    return 1
  fi
  # `docker inspect` (sem `container`) casa IMAGEM tambem — e a imagem tem o
  # mesmo nome do container. Com ele, este ramo achava que o container ja
  # existia e caia num `docker start` de algo inexistente: "No such container".
  if docker container inspect "$NOME" >/dev/null 2>&1; then
    docker start "$NOME" >/dev/null
    msg "container ja existia — iniciado"
    return 0
  fi
  # ATENCAO ao conjunto de flags abaixo: e alcance TOTAL, escolha consciente.
  # O racional: quem chega no /recovery ja tem shell root do host (o terminal
  # de recuperacao usa o mesmo HostShell do terminal principal), entao este
  # container nao amplia materialmente a superficie — ele so garante que o
  # Claude continue alcancando o que precisa consertar quando o host esta ruim.
  #
  # --restart always e o que faz este container ser INDEPENDENTE do
  # vps-manager: ele sobe no boot pelo proprio Docker, sem passar por nada
  # nosso.
  #
  # NAO existe ANTHROPIC_BASE_URL aqui. E a ausencia dela que tira este Claude
  # do claude-router; se alguem a acrescentar, a independencia acaba.
  docker run -d \
    --name "$NOME" \
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
    "$IMAGEM" >/dev/null
  msg "container $NOME criado e rodando"
}

down() {
  docker rm -f "$NOME" >/dev/null 2>&1 || true
  msg "container removido (o volume $VOLUME, com o login, permanece)"
}

status() {
  if ! docker container inspect "$NOME" >/dev/null 2>&1; then
    msg "estado:   ausente (rode: $0 up)"
    return 0
  fi
  local estado
  estado="$(docker container inspect -f '{{.State.Status}}' "$NOME")"
  msg "estado:   $estado"
  msg "reinicio: $(docker container inspect -f '{{.HostConfig.RestartPolicy.Name}}' "$NOME")"
  if [ "$estado" = "running" ]; then
    msg "claude:   $(docker exec "$NOME" claude --version 2>/dev/null || echo indisponivel)"
    if docker exec "$NOME" test -f /config/.credentials.json 2>/dev/null; then
      msg "login:    presente (credencial propria)"
    else
      msg "login:    AUSENTE — abra a aba Claude no /recovery e rode 'claude' uma vez"
    fi
  fi
}

doctor() {
  local falhas=0
  checa() { if [ "$2" = "ok" ]; then printf '  \033[32m✓\033[0m %s\n' "$1"; else printf '  \033[31m✗\033[0m %s\n' "$1"; falhas=$((falhas+1)); fi; }

  msg "== garantias do Claude de recuperacao =="
  docker container inspect "$NOME" >/dev/null 2>&1 && checa "container existe" ok || checa "container existe" nao
  [ "$(docker container inspect -f '{{.State.Status}}' "$NOME" 2>/dev/null)" = "running" ] \
    && checa "esta rodando" ok || checa "esta rodando" nao
  [ "$(docker container inspect -f '{{.HostConfig.RestartPolicy.Name}}' "$NOME" 2>/dev/null)" = "always" ] \
    && checa "sobe sozinho no boot (restart=always, independente do vps-manager)" ok \
    || checa "sobe sozinho no boot" nao
  # A garantia central: nenhuma variavel apontando o Claude para o router.
  if docker container inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$NOME" 2>/dev/null | grep -q '^ANTHROPIC_BASE_URL='; then
    checa "sem ANTHROPIC_BASE_URL (fora do claude-router)" nao
  else
    checa "sem ANTHROPIC_BASE_URL (fora do claude-router)" ok
  fi
  if docker exec "$NOME" sh -c 'grep -q ANTHROPIC_BASE_URL /config/settings.json 2>/dev/null' 2>/dev/null; then
    checa "settings.json do container tambem nao aponta pro router" nao
  else
    checa "settings.json do container tambem nao aponta pro router" ok
  fi
  # O login e passo MANUAL (device flow, uma vez). Faltar login nao e o mesmo
  # que garantia quebrada: e "ainda nao configurado". O codigo de saida separa
  # os dois para quem automatiza — 2 = so falta autenticar, 1 = algo regrediu.
  local semLogin=0
  if docker exec "$NOME" test -f /config/.credentials.json 2>/dev/null; then
    checa "login proprio (nao compartilha refresh token com o host)" ok
  else
    printf '  \033[33m•\033[0m %s\n' "login proprio — FALTA autenticar uma vez (rode 'claude' na aba do /recovery)"
    semLogin=1
  fi
  docker exec "$NOME" test -d /host/etc 2>/dev/null \
    && checa "enxerga o sistema de arquivos do host em /host" ok || checa "enxerga /host" nao
  docker exec "$NOME" sh -c 'command -v hostctl >/dev/null' 2>/dev/null \
    && checa "hostctl disponivel (systemctl/journalctl do host)" ok || checa "hostctl" nao
  docker exec "$NOME" sh -c 'command -v docker >/dev/null && docker ps >/dev/null 2>&1' 2>/dev/null \
    && checa "fala com o Docker do host" ok || checa "fala com o Docker do host" nao
  if [ "$falhas" != "0" ]; then erro "$falhas verificacao(oes) reprovada(s)"; return 1; fi
  if [ "$semLogin" != "0" ]; then msg "estrutura de pe; falta so o login."; return 2; fi
  msg "tudo de pe."
  return 0
}

case "${1:-status}" in
  build)   build ;;
  up)      up ;;
  down)    down ;;
  restart) docker restart "$NOME" >/dev/null && msg "reiniciado" ;;
  status)  status ;;
  doctor)  doctor ;;
  shell)   docker exec -it "$NOME" dtach -A /tmp/recovery.sock -E -z bash -l ;;
  *) erro "uso: $0 {build|up|down|restart|status|doctor|shell}"; exit 2 ;;
esac
