#!/bin/sh
# ponte-host — executa NO HOST o binario com o nome pelo qual fui chamado.
#
# Existe porque /opt/panel e montado neste container: o Claude le o
# CLAUDE.md e o .claude/settings.json do projeto, e os hooks de la chamam
# ferramentas do HOST (agentctl, graphify, vpsmctl, jira-api). Sem elas, TODA
# chamada de ferramenta imprimia "PreToolUse:Bash hook error ... not found" e
# enterrava a resposta do Claude em ruido.
#
# Instalado como symlink com varios nomes; $0 diz qual foi invocado.
#
# Regra de ouro deste arquivo: numa sessao de emergencia, uma ferramenta de
# CONVENIENCIA que nao existe nao pode derrubar nada. Por isso o caminho de
# falha e sempre "sai 0 em silencio", nunca "erro".
set -u
NOME="$(basename "$0")"

# Onde o binario costuma viver NO HOST. Primeiro que existir vence.
for c in "/usr/local/bin/$NOME" "/root/.local/bin/$NOME" "/usr/bin/$NOME" "/bin/$NOME"; do
  if [ -x "/host$c" ]; then
    ALVO="$c"
    break
  fi
done

if [ -z "${ALVO:-}" ]; then
  # Nao existe no host: silencio e sucesso. Um hook de conveniencia ausente nao
  # e motivo para reprovar a acao que o operador esta tentando fazer.
  exit 0
fi

# nsenter nos namespaces do PID 1 = rodar como se fosse no host. O cwd e
# preservado quando o caminho existe nos dois lados (/opt/panel e o caso
# normal); quando nao existe, cai na raiz em vez de falhar.
DIR="$(pwd 2>/dev/null || echo /)"
[ -d "/host$DIR" ] || [ -d "$DIR" ] || DIR=/
exec nsenter -t 1 -m -u -i -n -p -- /bin/sh -c 'cd "$1" 2>/dev/null || cd /; shift; exec "$@"' _ "$DIR" "$ALVO" "$@"
