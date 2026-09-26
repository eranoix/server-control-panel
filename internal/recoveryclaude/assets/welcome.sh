#!/bin/bash
# First screen of the "Claude (independent)" tab in /recovery: where you are,
# what this environment reaches, and what to do when the login is missing.
echo
echo -e "\033[1;36m  Recovery Claude: independent connection\033[0m"
echo -e "  \033[2mDirect API connection and its own login: it keeps working"
echo -e "  when the host Claude setup, the panel or the host install are broken.\033[0m"
echo
echo -e "  \033[1mReach:\033[0m /opt/panel (read and write), the host filesystem at \033[1m/host\033[0m,"
echo -e "  containers via \033[1mdocker\033[0m, and host commands via \033[1mhostctl\033[0m, e.g.:"
echo -e "    \033[2mhostctl systemctl status server-control-panel\033[0m"
echo -e "    \033[2mhostctl journalctl -u server-control-panel -n 50\033[0m"
echo
if [ ! -f "${CLAUDE_CONFIG_DIR:-/config}/.credentials.json" ]; then
  echo -e "  \033[1;33mThis environment has no login yet.\033[0m Run \033[1mclaude\033[0m and authenticate once;"
  echo -e "  the credential lives in the volume and survives restarts and rebuilds."
  echo
fi
