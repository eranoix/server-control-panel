# Claude de RECUPERAÇÃO — conexão independente
#
# Por que este container existe: no host, TODO `claude` passa pelo
# claude-router (ANTHROPIC_BASE_URL=http://127.0.0.1:8788 no settings.json do
# config-dir). O router é um serviço a mais no caminho — e um serviço a mais é
# uma coisa a mais que pode estar quebrada justamente na hora em que se recorre
# à tela de recuperação. Aqui dentro NÃO existe ANTHROPIC_BASE_URL: a conexão
# sai direto para a API da Anthropic. Se o router (ou o vps-manager) estiver de
# joelhos, este Claude continua respondendo.
#
# A independência tambem vale para a CREDENCIAL: o config-dir deste container e
# um volume proprio (/config), com login proprio. Compartilhar o
# .credentials.json do host faria os dois lados renovarem o mesmo refresh token
# e se invalidarem — a ferramenta de emergencia poderia derrubar o Claude
# principal, que e o oposto do objetivo.
FROM ubuntu:24.04

# Ubuntu 24.04 = mesma glibc do host (2.39). O binario do Claude e nativo e
# dinamicamente ligado; base diferente = "not found" no melhor caso.
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl git dtach bash jq less nano vim-tiny procps \
      iproute2 iputils-ping net-tools dnsutils util-linux ripgrep unzip \
      python3 python3-venv \
 && rm -rf /var/lib/apt/lists/*

# Cliente do Docker (o socket do host e montado): permite ver/reiniciar
# containers de dentro da sessao de recuperacao.
RUN curl -fsSL https://download.docker.com/linux/static/stable/x86_64/docker-27.5.1.tgz \
      -o /tmp/d.tgz \
 && tar -xzf /tmp/d.tgz -C /tmp \
 && install -m 0755 /tmp/docker/docker /usr/local/bin/docker \
 && rm -rf /tmp/d.tgz /tmp/docker

# Claude Code, instalado AQUI DENTRO. Baixar em vez de copiar do host e
# deliberado: o container nao pode depender da instalacao do host, que e uma
# das coisas que ele existe para consertar.
RUN curl -fsSL https://claude.ai/install.sh | bash \
 && ln -sf /root/.local/bin/claude /usr/local/bin/claude \
 && /usr/local/bin/claude --version

# hostctl: entra nos namespaces do PID 1 do host. Com --pid=host --privileged,
# e o que faz `hostctl systemctl restart vps-manager` e
# `hostctl journalctl -u vps-manager` funcionarem de dentro do container.
RUN printf '%s\n' '#!/bin/sh' \
  '# Executa o comando NO HOST (namespaces do PID 1). Requer --pid=host --privileged.' \
  'exec nsenter -t 1 -m -u -i -n -p -- "$@"' > /usr/local/bin/hostctl \
 && chmod +x /usr/local/bin/hostctl

# Ferramentas do host que os HOOKS do projeto chamam.
#
# /opt/panel e montado aqui, entao o Claude le o CLAUDE.md e o
# .claude/settings.json do projeto — e os hooks de la invocam `agentctl` e
# `/root/.local/bin/graphify`, que sao binarios DO HOST. Sem eles, TODA chamada
# de ferramenta imprimia "PreToolUse:Bash hook error ... not found", enterrando
# a resposta do Claude em ruido (foi o que o operador viu na primeira sessao
# real).
#
# A saida NAO e desligar os hooks: e dar as ferramentas de verdade. Como o
# container roda com --pid=host --privileged, um wrapper com nsenter executa o
# binario original DO HOST. Se ele nao existir la (host diferente, ferramenta
# removida), o wrapper sai 0 em silencio — um hook de conveniencia nao pode
# derrubar a sessao de emergencia.
COPY ponte-host.sh /usr/local/bin/ponte-host
RUN chmod +x /usr/local/bin/ponte-host \
 && mkdir -p /root/.local/bin \
 && ln -sf /usr/local/bin/ponte-host /usr/local/bin/agentctl \
 && ln -sf /usr/local/bin/ponte-host /usr/local/bin/vpsmctl \
 && ln -sf /usr/local/bin/ponte-host /root/.local/bin/graphify \
 && ln -sf /usr/local/bin/ponte-host /usr/local/bin/graphify \
 && ln -sf /usr/local/bin/ponte-host /usr/local/bin/jira-api

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
COPY bemvindo.sh /usr/local/bin/bemvindo.sh
RUN chmod +x /usr/local/bin/entrypoint.sh /usr/local/bin/bemvindo.sh

# O config-dir do Claude e um VOLUME: e onde mora o login proprio deste
# container, e ele precisa sobreviver a `docker rm`/rebuild da imagem.
ENV CLAUDE_CONFIG_DIR=/config
ENV TERM=xterm-256color
VOLUME ["/config"]
WORKDIR /opt/panel

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
