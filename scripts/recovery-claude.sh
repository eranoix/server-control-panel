#!/usr/bin/env bash
# recovery-claude.sh — atalho para o gerenciador do Claude de recuperação.
#
# A implementação NÃO vive aqui: ela é `internal/recoveryclaude/assets/manage.sh`,
# ao lado do Dockerfile que ela constrói. Isso não é organização por gosto — é o
# que permite ao BINÁRIO embutir o conjunto inteiro e materializá-lo em
# <DataDir>/recovery-claude/ na hora de usar.
#
# Por que isso importa: o botão "Ligar o container" chamava um script
# do repositório e recebia "no such file or directory", porque o deploy builda
# do worktree do ticket enquanto /opt/panel está noutra branch. Um
# processo no ar não pode confiar no conteúdo do diretório de trabalho do
# repositório — o binário é a unidade de deploy, então o que ele precisa viaja
# com ele. Este arquivo continua existindo só para o comando documentado seguir
# funcionando de dentro do repo.
#
# Uso: scripts/recovery-claude.sh {build|up|down|restart|status|doctor|shell}
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/internal/recoveryclaude/assets/manage.sh" "$@"
