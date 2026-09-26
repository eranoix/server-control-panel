# Recovery Claude: an independent connection.
#
# The host Claude may be configured to reach the API through a proxy
# (ANTHROPIC_BASE_URL), one more service that can be broken exactly when the
# recovery screen is needed. This image sets no base URL, so the connection goes
# straight to the API.
#
# The credential is independent too: the config dir is its own volume (/config)
# with its own login. Sharing the host's .credentials.json would make both sides
# refresh the same token and invalidate each other.
FROM ubuntu:24.04

# Ubuntu 24.04 has the host's glibc (2.39); the Claude binary is native and
# dynamically linked.
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl git dtach bash jq less nano vim-tiny procps \
      iproute2 iputils-ping net-tools dnsutils util-linux ripgrep unzip \
      python3 python3-venv \
 && rm -rf /var/lib/apt/lists/*

# Docker client (the host socket is mounted) to inspect/restart containers.
RUN curl -fsSL https://download.docker.com/linux/static/stable/x86_64/docker-27.5.1.tgz \
      -o /tmp/d.tgz \
 && tar -xzf /tmp/d.tgz -C /tmp \
 && install -m 0755 /tmp/docker/docker /usr/local/bin/docker \
 && rm -rf /tmp/d.tgz /tmp/docker

# Installed here rather than copied from the host: the host install is one of
# the things this container exists to repair.
RUN curl -fsSL https://claude.ai/install.sh | bash \
 && ln -sf /root/.local/bin/claude /usr/local/bin/claude \
 && /usr/local/bin/claude --version

# hostctl: enters the host PID 1 namespaces (needs --pid=host --privileged), so
# `hostctl systemctl restart server-control-panel` works from inside the container.
RUN printf '%s\n' '#!/bin/sh' \
  '# Runs the command ON THE HOST (PID 1 namespaces). Needs --pid=host --privileged.' \
  'exec nsenter -t 1 -m -u -i -n -p -- "$@"' > /usr/local/bin/hostctl \
 && chmod +x /usr/local/bin/hostctl

# Host tools called by the project's hooks (the project in /opt/panel is
# mounted here). Each name is a symlink to a wrapper that runs the HOST binary
# through nsenter, and exits 0 silently when it does not exist there.
COPY host-bridge.sh /usr/local/bin/host-bridge
RUN chmod +x /usr/local/bin/host-bridge \
 && mkdir -p /root/.local/bin \
 && ln -sf /usr/local/bin/host-bridge /usr/local/bin/panelctl \
 && ln -sf /usr/local/bin/host-bridge /root/.local/bin/graphify \
 && ln -sf /usr/local/bin/host-bridge /usr/local/bin/graphify \
 && ln -sf /usr/local/bin/host-bridge /usr/local/bin/jira-api

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
COPY welcome.sh /usr/local/bin/welcome.sh
RUN chmod +x /usr/local/bin/entrypoint.sh /usr/local/bin/welcome.sh

# The config dir is a VOLUME so this container's login survives `docker rm`
# and image rebuilds.
ENV CLAUDE_CONFIG_DIR=/config
ENV TERM=xterm-256color
VOLUME ["/config"]
WORKDIR /opt/panel

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
