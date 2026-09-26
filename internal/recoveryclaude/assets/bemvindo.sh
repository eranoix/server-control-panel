#!/bin/bash
# Primeira tela da aba "Claude (independente)" do /recovery. Diz onde a pessoa
# esta, o que este ambiente alcanca e o que fazer se faltar login — porque numa
# emergencia ninguem lembra do procedimento.
echo
echo -e "\033[1;36m  Claude de recuperacao — conexao independente\033[0m"
echo -e "  \033[2mSem claude-router no caminho e com login proprio: continua funcionando"
echo -e "  se o router, o vps-manager ou a instalacao do host estiverem quebrados.\033[0m"
echo
echo -e "  \033[1mAlcance:\033[0m /opt/panel (leitura e escrita), sistema de arquivos do host em \033[1m/host\033[0m,"
echo -e "  containers via \033[1mdocker\033[0m, e comandos do host via \033[1mhostctl\033[0m — ex.:"
echo -e "    \033[2mhostctl systemctl status vps-manager\033[0m"
echo -e "    \033[2mhostctl journalctl -u vps-manager -n 50\033[0m"
echo
if [ ! -f "${CLAUDE_CONFIG_DIR:-/config}/.credentials.json" ]; then
  echo -e "  \033[1;33mFalta o login deste ambiente.\033[0m Rode \033[1mclaude\033[0m e autentique uma vez;"
  echo -e "  a credencial fica no volume e sobrevive a restart e rebuild."
  echo
fi
