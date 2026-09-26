# Contrato de deploy

`scripts/deploy.sh` deixou de conhecer o nome de um projeto. Ele executa um
contrato — **gate → build → health → symlink → rollback** — parametrizado por um
arquivo `deploy/<projeto>.conf`.

```sh
scripts/deploy.sh --conf deploy/painel.conf <novo-binario>   # deploy
scripts/deploy.sh --conf deploy/painel.conf --rollback       # volta ao anterior
scripts/deploy.sh --conf deploy/painel.conf --validar        # só valida a conf
```

Sem `--conf`, o padrão é `deploy/painel.conf` (trás-compatibilidade). O arquivo
ainda precisa existir: default não é permissão para rodar sem configuração.

## Duas regras que evitam os dois erros mais caros

**1. O `agentctl` executa o `deploy.sh` do working tree PRINCIPAL** (`$ROOT`).
Editar este script numa worktree, rodar `agentctl deploy` de lá e ver verde
significa que o script **velho** rodou. Integre antes de provar. Isso já queimou
uma sessão neste repositório, e o comentário está no próprio `deploy.sh`.

**2. O contrato roda ONDE O ARTEFATO ATERRISSA, não onde ele foi construído.**
É para isso que `BUILD_CMD` vazio existe: o binário pode ser compilado noutra
máquina e chegar pronto. O `lab-agent` (08-08) é construído no VPS e aterrissa no
CT 201 — e o `deploy.sh` não precisa saber o que é rede para isso funcionar.

## Código de saída

| Código | Significado |
|---|---|
| `0` | deploy saudável |
| `1` | falhou e **voltou sozinho** — o rollback deu certo e o serviço está de pé |
| `2` ou mais | quebrado: configuração inválida, ou o rollback também falhou |

O `1` é **deliberado**. Quem chama não pode lê-lo como falha total: é a diferença
entre "o serviço está no ar na versão anterior" e "o serviço está fora do ar". O
08-08 depende exatamente dessa distinção para provar o auto-rollback.

## Chaves

### Obrigatórias — sem qualquer uma delas o script morre nomeando a chave

| Chave | O que é |
|---|---|
| `PROJ_SRC` | onde o código está (worktree de build) |
| `PROJ_ROOT` | onde o artefato aterrissa — **pode diferir** de `PROJ_SRC` |
| `BIN_DIR` | diretório dos binários carimbados |
| `LINK` | o symlink trocado atomicamente |
| `ARTIFACT_PREFIX` | prefixo usado pela retenção e pela busca do rollback |
| `HEALTH_MODE` | `url` ou `cmd` |
| `HEALTH_TRIES` · `HEALTH_INTERVAL` | a janela de saúde é `TRIES × INTERVAL` segundos |
| `SERVICE` | unit principal (`restart` + `reset-failed`) |
| `KEEP_BINARIES` | retenção — **mínimo 2**, ver abaixo |
| `LOCK_FILE` · `LOG` | trava do `flock` e log do deploy |

`HEALTH_MODE=url` exige `HEALTH_URL`; `HEALTH_MODE=cmd` exige `HEALTH_CMD`.

**`KEEP_BINARIES < 2` é erro DURO, não aviso.** O rollback precisa de pelo menos
dois binários prévios. Um projeto configurado com 1 descobriria isso no pior
momento possível: com o binário quebrado no ar e nada para onde voltar.

### Opcionais — **vazio tem significado declarado**

Vazio com significado implícito é como um projeto novo ganha comportamento que
ninguém pediu. Por isso cada um está escrito aqui e no comentário do bloco
correspondente do `deploy.sh`.

| Chave | Vazio significa |
|---|---|
| `BUILD_CMD` | **projeto SEM etapa de build** — o artefato chega pronto. Caso de primeira classe, não exceção |
| `INV_FILE` | sem pino textual de invariantes |
| `STATE_BACKUP_DIR` / `STATE_FILES` | pula a rotação de backups de estado (específico do painel) |
| `DRAIN_URL` | sem drenagem de fila — e com isso **some a dependência de `jq`** |
| `PREFLIGHT_CMDS` | sem pré-voo específico do projeto |
| `BIN_CHECK_ARG` | não roda autoteste no binário novo antes da troca |
| `EXTRA_ARTIFACTS` | nenhum artefato extra aplicado depois do health |
| `EXTRA_SERVICES` | nenhum serviço extra reiniciado depois do health |

Outros padrões: `KEEP_STATE_BACKUPS=10`, `DRAIN_TRIES=60`, `DRAIN_INTERVAL=3`,
`HEALTH_CMD_TIMEOUT=10`.

### `EXTRA_ARTIFACTS`

Uma linha por artefato, `origem|destino|serviço-opcional`. Aplicados **depois** do
health passar, para que um build quebrado nunca sobrescreva uma ferramenta que
funciona. `$BUILD_DIR` está disponível na conf e aponta para o diretório do
binário novo.

## `HEALTH_MODE=cmd`

Existe porque **a sonda correta de um projeto pode não ser um GET**. Pode ser um
autoteste que exercita o caminho real (`--selftest`), enquanto um GET em
`/healthz` só prova que o processo respondeu. E pode haver projeto sem superfície
HTTP nenhuma.

(A justificativa original da decisão dizia que o `tl-agent` é binário único e não
tem HTTP. Medido: ele **não** é binário único e **tem** HTTP no CT 201. O desenho
continua certo; o argumento foi corrigido.)

Cada tentativa roda sob `timeout $HEALTH_CMD_TIMEOUT` e é registrada no log — sem
isso, um health-gate que aprova por engano fica indistinguível de um que aprova
certo.

## O que NÃO faz parte do contrato

O avanço do canônico (`refactor/foundation`), o board `.claude/coord/` e a
propagação entre worktrees continuam no `agentctl`. São **coordenação
multi-sessão do repositório do painel**, não deploy: um projeto que não é o painel
não tem canônico para avançar nem worktrees de outras sessões para propagar.

A fronteira está comentada no `agentctl`, no ponto exato onde ela passa.

## Pinos

`scripts/tests/deploy-conf.sh` — 20 asserções sobre conf ausente, chave
obrigatória faltando, `HEALTH_MODE` inválido, `KEEP_BINARIES < 2`, `BUILD_CMD`
vazio, e `HEALTH_MODE=cmd` nos dois sentidos. Inclui **controle negativo**: uma
conf válida e completa tem de passar — pino que só sabe reprovar não mede nada.

Rodam dentro do `agentctl gate` (mesmo passo dos invariantes, porque são shell e
não pacote Go) e **não tocam serviço real**: usam `--validar`, que sai antes do
primeiro efeito colateral.

## Cobertura por projeto — e o que cada um NÃO cobre

Três projetos usam este contrato, e **nenhum deles o exercita inteiro**. A tabela
existe porque "o contrato está provado" lido sem ela vira falso-verde por redação:
alguém supõe que um projeto sozinho cobriu tudo.

| Parte do contrato | painel | lab-agent (08-08) | tl-agent (08-09) |
|---|---|---|---|
| Etapa de **build** | ✅ (no `agentctl`) | ✅ **é o que ele cobre** — Go, no VPS | ❌ **não tem** — é Python |
| `BUILD_CMD` vazio | ✅ | ✅ (build é passo anterior) | ✅ (não há o que compilar) |
| Saúde por **URL** | ✅ | ✅ | ❌ |
| Saúde por **COMANDO** | ❌ | ❌ | ✅ **é o que ele cobre** |
| Travessia **para outra máquina** | ❌ (local) | ✅ | ✅ |
| Troca por symlink de **binário** | ✅ | ✅ | ❌ |
| Troca por symlink de **script** | ❌ | ❌ | ✅ |
| **Auto-rollback** nos três sinais | — | ✅ | ✅ |
| `EXTRA_SERVICES` | ❌ (usa `EXTRA_ARTIFACTS`) | ❌ | ✅ (`tl-daemon`) |
| Backup de estado / drenagem de fila | ✅ | ❌ | ❌ |

**O que NENHUM dos três cobre**, dito com todas as letras: `INV_FILE` fora do
painel, e `BIN_CHECK_ARG` fora do painel.

### A limitação de escopo do artefato único

O contrato troca **um arquivo** atomicamente (`install -m 0755` no Step 4). Para o
painel e o `lab-agent` isso é o programa inteiro. Para o `tl-agent` **não é**: o
artefato trocado é o *entrypoint*, e os arquivos de apoio (`tl_agent.py`,
`profile.json`, …) viajam pela entrega com hash conferido, mas **ficam fora da
troca atômica e fora do rollback**.

Consequência prática, para ninguém descobrir isso no pior momento: um deploy que
falha e volta sozinho devolve o **entrypoint** anterior, e os arquivos de apoio
permanecem na versão nova. Quem cobre essa diferença é o
`deploy/tl-agent/procedencia.sh` (instalado == versionado), não o rollback.

### `LINK` pode morar fora de `BIN_DIR` — mas isso já quebrou uma vez

Até o 08-09, todo projeto tinha o `LINK` dentro do `BIN_DIR`, e o `swap_symlink`
criava um alvo **relativo** que funcionava por acidente de layout. O `tl-agent`
tem o `LINK` na raiz e as versões em `versoes/`, e o mesmo código produziu um
symlink apontando para um irmão inexistente: o serviço subiu, morreu com
`No such file or directory`, esgotou o `StartLimit` — e, por ser o **primeiro**
deploy, não havia versão anterior para onde voltar. Corrigido no `swap_symlink`
(o alvo é resolvido relativo a onde o `LINK` mora) e congelado por pino em
`scripts/tests/deploy-conf.sh`.
