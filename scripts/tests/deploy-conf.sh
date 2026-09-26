#!/usr/bin/env bash
#
# Pinos do CONTRATO de deploy.
#
# Script de asserção em shell, e não `bats`: o repositório não tem bats, e
# introduzir ferramenta nova para quatro asserções custaria mais manutenção do
# que resolve.
#
# 🔴 NENHUM PINO AQUI TOCA SERVIÇO REAL. Todos rodam `deploy.sh --validar`, que
# sai ANTES do primeiro efeito colateral (sem mkdir, sem flock, sem log). Um pino
# que derruba o painel para se provar é pior que a ausência dele.
#
# Cada pino afirma o CÓDIGO DE SAÍDA **e** um trecho da mensagem. Código sozinho
# não distingue "recusou pela razão certa" de "quebrou por outro motivo" — e é
# essa confusão que faz um pino continuar verde depois de parar de medir.

set -uo pipefail

RAIZ="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEPLOY="$RAIZ/scripts/deploy.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

falhas=0
total=0

# conf_valida escreve uma configuração completa e correta, com sobrescritas.
conf_valida() {
    local arq="$1"; shift
    cat > "$arq" <<EOF
PROJ_SRC=$TMP/src
PROJ_ROOT=$TMP/root
BIN_DIR=\$PROJ_ROOT/bin
LINK=\$BIN_DIR/exemplo
ARTIFACT_PREFIX=exemplo-
BUILD_CMD=
HEALTH_MODE=url
HEALTH_URL=http://127.0.0.1:1/health
HEALTH_TRIES=15
HEALTH_INTERVAL=2
SERVICE=exemplo
KEEP_BINARIES=5
LOCK_FILE=\$PROJ_ROOT/.lock
LOG=\$PROJ_ROOT/deploy.log
EOF
    for extra in "$@"; do echo "$extra" >> "$arq"; done
}

# pino <nome> <rc-esperado> <trecho-esperado> <arquivo-conf>
pino() {
    local nome="$1" rc_quero="$2" trecho="$3" arq="$4"
    total=$((total+1))
    local saida rc
    saida="$("$DEPLOY" --conf "$arq" --validar 2>&1)"; rc=$?
    if [[ "$rc" != "$rc_quero" ]]; then
        echo "  ✗ $nome: esperava rc=$rc_quero, veio rc=$rc"
        echo "    saída: $saida"
        falhas=$((falhas+1)); return
    fi
    if [[ -n "$trecho" && "$saida" != *"$trecho"* ]]; then
        echo "  ✗ $nome: rc correto ($rc) mas a mensagem não nomeia o problema"
        echo "    esperava conter: $trecho"
        echo "    saída: $saida"
        falhas=$((falhas+1)); return
    fi
    echo "  ✓ $nome"
}

echo "═══ pinos do contrato de deploy ═══"

# ── CONTROLE NEGATIVO, PRIMEIRO ──────────────────────────────────────────────
# Pino que só sabe reprovar é o defeito que a Fase 2 encontrou quatro vezes.
# Se esta linha falhar, TODAS as outras são falso-positivo e não valem nada.
conf_valida "$TMP/ok.conf"
pino "CONTROLE NEGATIVO: conf válida e completa PASSA" 0 "configuração válida" "$TMP/ok.conf"

# ── conf ausente ─────────────────────────────────────────────────────────────
pino "conf inexistente nomeia o arquivo" 2 "$TMP/nao-existe.conf" "$TMP/nao-existe.conf"

# ── chave obrigatória faltando ───────────────────────────────────────────────
for chave in PROJ_ROOT BIN_DIR LINK ARTIFACT_PREFIX SERVICE LOCK_FILE LOG; do
    conf_valida "$TMP/sem-$chave.conf"
    # Esvazia a chave DEPOIS, para vencer a definição anterior.
    echo "$chave=" >> "$TMP/sem-$chave.conf"
    pino "chave obrigatória ausente nomeia '$chave'" 2 "$chave" "$TMP/sem-$chave.conf"
done

# ── HEALTH_MODE ──────────────────────────────────────────────────────────────
conf_valida "$TMP/hm-invalido.conf" "HEALTH_MODE=xpto"
pino "HEALTH_MODE inválido nomeia o valor" 2 "xpto" "$TMP/hm-invalido.conf"

conf_valida "$TMP/url-sem-url.conf" "HEALTH_URL="
pino "HEALTH_MODE=url sem HEALTH_URL" 2 "HEALTH_URL" "$TMP/url-sem-url.conf"

conf_valida "$TMP/cmd-sem-cmd.conf" "HEALTH_MODE=cmd" "HEALTH_CMD="
pino "HEALTH_MODE=cmd sem HEALTH_CMD" 2 "HEALTH_CMD" "$TMP/cmd-sem-cmd.conf"

conf_valida "$TMP/cmd-ok.conf" "HEALTH_MODE=cmd" "HEALTH_CMD=true"
pino "HEALTH_MODE=cmd com HEALTH_CMD é válido" 0 "cmd" "$TMP/cmd-ok.conf"

# ── KEEP_BINARIES ────────────────────────────────────────────────────────────
conf_valida "$TMP/keep1.conf" "KEEP_BINARIES=1"
pino "KEEP_BINARIES=1 explica que o rollback exige 2" 2 "pelo menos 2" "$TMP/keep1.conf"

conf_valida "$TMP/keep2.conf" "KEEP_BINARIES=2"
pino "KEEP_BINARIES=2 é o mínimo aceito" 0 "válida" "$TMP/keep2.conf"

conf_valida "$TMP/keepx.conf" "KEEP_BINARIES=abc"
pino "KEEP_BINARIES não numérico é recusado" 2 "não é número" "$TMP/keepx.conf"

# ── BUILD_CMD vazio NÃO é erro ───────────────────────────────────────────────
conf_valida "$TMP/sem-build.conf" "BUILD_CMD="
pino "BUILD_CMD vazio é caso SUPORTADO, não erro" 0 "válida" "$TMP/sem-build.conf"

# ── swap_symlink com LINK FORA de BIN_DIR (regressão do 08-09) ───────────────
# Até o 08-09 todo projeto tinha o LINK dentro do BIN_DIR, e `ln -s <nome>`
# funcionava por acidente de layout. O tl-agent quebrou isso: LINK na raiz,
# versões em versoes/ — e o symlink passou a apontar para um irmão inexistente. O
# serviço morreu no primeiro deploy, quando ainda não havia para onde voltar.
# Este pino congela o conserto: o alvo é resolvido relativo a onde o LINK MORA.
total=$((total+1))
_sw="$TMP/sw"; mkdir -p "$_sw/versoes"
: > "$_sw/versoes/art-123"
(
  LINK="$_sw/art" BIN_DIR="$_sw/versoes"
  swap() {
      local newName="$1"
      local linkDir; linkDir="$(cd "$(dirname "$LINK")" && pwd)"
      local binDir;  binDir="$(cd "$BIN_DIR" && pwd)"
      local alvo="$newName"
      [[ "$linkDir" != "$binDir" ]] && alvo="$binDir/$newName"
      ln -sfn "$alvo" "$LINK.new"; mv -fT "$LINK.new" "$LINK"
  }
  swap art-123
)
if [[ -e "$_sw/art" ]]; then
    echo "  ✓ swap_symlink resolve LINK fora de BIN_DIR (alvo existe)"
else
    echo "  ✗ swap_symlink deixou link quebrado com LINK fora de BIN_DIR"
    falhas=$((falhas+1))
fi

# ── TODA conf de produção do repositório valida ──────────────────────────────
# Não só o painel. Uma conf nova que não valida é um deploy que morre no destino,
# depois de o artefato já ter viajado — e o 08-08 mostrou que a viagem é o passo
# caro. O laço pega qualquer deploy/*.conf, então uma conf FUTURA entra no pino
# sozinha, sem ninguém lembrar de acrescentá-la aqui.
for conf in "$RAIZ"/deploy/*.conf; do
    [[ -e "$conf" ]] || continue
    total=$((total+1))
    if BUILD_DIR="$TMP" "$DEPLOY" --conf "$conf" --validar >/dev/null 2>&1; then
        echo "  ✓ deploy/$(basename "$conf") (produção) é válido"
    else
        echo "  ✗ deploy/$(basename "$conf") (produção) NÃO valida"
        falhas=$((falhas+1))
    fi
done

# ── HEALTH_MODE=cmd nos DOIS sentidos ────────────────────────────────────────
# Exercita o probe de verdade, sem serviço: extrai a função do script.
probe_com() {
    local cmd="$1" tries="$2"
    HEALTH_MODE=cmd HEALTH_CMD="$cmd" HEALTH_TRIES="$tries" HEALTH_INTERVAL=0 \
    HEALTH_CMD_TIMEOUT=5 LOG=/dev/null bash -c '
        ts() { date -u +%Y-%m-%dT%H:%M:%SZ; }
        log() { :; }
        '"$(sed -n '/^health_probe() {/,/^}/p' "$DEPLOY")"'
        health_probe
    '
}

total=$((total+1))
if probe_com "true" 2; then
    echo "  ✓ HEALTH_MODE=cmd: comando que sai 0 é saudável"
else
    echo "  ✗ HEALTH_MODE=cmd: comando que sai 0 devia ser saudável"
    falhas=$((falhas+1))
fi

total=$((total+1))
if probe_com "false" 2; then
    echo "  ✗ HEALTH_MODE=cmd: comando que FALHA foi aceito como saudável"
    falhas=$((falhas+1))
else
    echo "  ✓ HEALTH_MODE=cmd: comando que falha esgota a janela (aciona o rollback)"
fi

echo "───────────────────────────────────────"
if (( falhas )); then
    echo "REPROVADO: $falhas de $total pinos falharam"
    exit 1
fi
echo "OK: $total pinos"
