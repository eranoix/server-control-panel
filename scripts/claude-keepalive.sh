#!/usr/bin/env bash
# claude-keepalive — mantém a janela de uso de 5h do plano Max "sempre ligada".
#
# A janela de 5h é estado no servidor da Anthropic: liga na 1ª mensagem a um
# modelo e reseta 5h depois. Sem atividade após o reset, o contador fica zerado
# parado. Este script, disparado de hora em hora pelo agendador (um job por
# conta), só gasta um ping QUANDO a janela está ociosa — então o disparo horário
# é quase sempre um no-op barato e religa o contador em ≤1h após qualquer reset
# (inclusive corrigindo uso manual fora de fase).
#
# Uso:  claude-keepalive <jordan|sam>
#
# Gate (decidir se precisa pingar):
#   1) lê o accessToken da conta e consulta GET /api/oauth/usage (read-only);
#   2) se five_hour.resets_at está no futuro  → janela ATIVA  → NÃO pinga;
#      se ausente/null/no passado             → janela OCIOSA → pinga;
#   3) se a consulta falhar (rede/429)        → fallback: pinga só se o último
#      ping foi há mais de 4h45m (state file) — evita spam e mantém vivo mesmo
#      com o endpoint fora.
#
# O ping em si é `claude -p` mínimo no modelo mais barato (haiku), o que de fato
# inicia/renova a janela (consultar /oauth/usage NÃO liga o contador).
#
# Segurança: o token é lido do .credentials.json da conta (mesma fronteira de
# confiança de ratelimits.go) e NUNCA é logado.
#
# Login da conta: o refreshToken tem VALIDADE FIXA (refreshTokenExpiresAt).
# Venceu, o CLI zera a credencial e a conta fica deslogada até um login novo
# pelo navegador — aconteceu com sam (30/jul) e jordan (24/set), e em ambos o
# job falhou de hora em hora com um "ping falhou (rc=1)" sem causa. Agora:
#   - credencial zerada/vencida → erro nomeando a causa e o conserto;
#   - faltando < 48h pro login vencer → o job FALHA uma vez a cada 12h com
#     aviso (a falha do job é o canal de notificação que chega ao usuário);
#   - o erro do `claude -p` vai pro log (a saída do CLI não contém token).

set -uo pipefail

USAGE_URL="https://api.anthropic.com/api/oauth/usage"
STATE_DIR="${VPSM_KEEPALIVE_DIR:-/opt/panel/data/keepalive}"
PING_PROMPT="responda apenas: ok"
PING_MODEL="${VPSM_KEEPALIVE_MODEL:-haiku}"
FALLBACK_MIN_AGE=17100   # 4h45m em segundos: idade mínima p/ pingar no fallback
PING_TIMEOUT=120

log() { printf '%s [keepalive:%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "${ACCT:-?}" "$*"; }

ACCT="${1:-}"
if [ -z "$ACCT" ]; then
  echo "uso: claude-keepalive <jordan|sam>" >&2
  exit 2
fi

# Espelha claudeacct.defaultRegistry: jordan usa o config default ($HOME/.claude),
# sam usa um CLAUDE_CONFIG_DIR dedicado.
export HOME=/root
CFG_DIR=""
# Nunca herdar a conta do ambiente: rodado de dentro de uma sessão sam,
# CLAUDE_CONFIG_DIR apontava pra sam e o "keepalive jordan" pingava a conta
# errada (e passava).
unset CLAUDE_CONFIG_DIR
case "$ACCT" in
  jordan)
    CRED="/root/.claude/.credentials.json"
    ;;
  sam)
    CFG_DIR="/srv/agent-accounts/sam"
    CRED="$CFG_DIR/.credentials.json"
    ;;
  *)
    log "conta desconhecida: $ACCT (use jordan|sam)"
    exit 2
    ;;
esac

# Resolve o binário do claude (o PATH do daemon pode não ter ~/.local/bin).
CLAUDE_BIN="$(command -v claude 2>/dev/null || true)"
[ -z "$CLAUDE_BIN" ] && [ -x /root/.local/bin/claude ] && CLAUDE_BIN=/root/.local/bin/claude
if [ -z "$CLAUDE_BIN" ]; then
  log "ERRO: binário 'claude' não encontrado"
  exit 1
fi

mkdir -p "$STATE_DIR" 2>/dev/null || true
LAST_FILE="$STATE_DIR/last-$ACCT"
WARN_FILE="$STATE_DIR/login-warn-$ACCT"
now="$(date +%s)"
WARN_WINDOW=172800   # 48h: antecedência do aviso de login vencendo
WARN_EVERY=43200     # 12h: intervalo mínimo entre avisos

relogin_hint() {
  if [ -n "$CFG_DIR" ]; then
    echo "refaça o login: CLAUDE_CONFIG_DIR=$CFG_DIR claude auth login --claudeai"
  else
    echo "refaça o login: claude auth login --claudeai (HOME=/root)"
  fi
}

# ── Estado do login (sem ler nem imprimir segredo: só presença e datas) ─────
login_state="$(CRED="$CRED" python3 -c 'import json,os,time
try:
    o=json.load(open(os.environ["CRED"])).get("claudeAiOauth") or {}
except Exception:
    print("sem-credencial 0"); raise SystemExit
if not o.get("refreshToken"):
    print("deslogada 0"); raise SystemExit
exp=int((o.get("refreshTokenExpiresAt") or 0)/1000)
print("ok", exp)' 2>/dev/null || echo "sem-credencial 0")"
login_status="${login_state%% *}"
login_exp="${login_state##* }"
case "$login_status" in
  sem-credencial)
    log "ERRO: credencial da conta não encontrada/ilegível ($CRED) — $(relogin_hint)"
    exit 1 ;;
  deslogada)
    log "ERRO: conta deslogada — o login venceu e o CLI zerou a credencial; $(relogin_hint)"
    exit 1 ;;
esac
login_warn=""
if [ "${login_exp:-0}" -gt 0 ]; then
  left=$((login_exp - now))
  if [ "$left" -le 0 ]; then
    log "ERRO: o login da conta venceu em $(date -d "@$login_exp" '+%d/%m %H:%M'); $(relogin_hint)"
    exit 1
  elif [ "$left" -le "$WARN_WINDOW" ]; then
    last_warn="$(cat "$WARN_FILE" 2>/dev/null || echo 0)"
    case "$last_warn" in ''|*[!0-9]*) last_warn=0 ;; esac
    if [ $((now - last_warn)) -ge "$WARN_EVERY" ]; then
      login_warn="AVISO: o login da conta vence em $((left/3600))h ($(date -d "@$login_exp" '+%d/%m %H:%M')) — depois disso ela desloga; $(relogin_hint)"
    fi
  fi
fi
# O aviso sai no FIM (depois do ping), pra não impedir o keep-alive de hoje.
finish() {
  if [ -n "$login_warn" ]; then
    log "$login_warn"
    echo "$now" > "$WARN_FILE" 2>/dev/null || true
    exit 1
  fi
  exit "${1:-0}"
}

# ── Gate: a janela de 5h está ociosa? ────────────────────────────────────────
token=""
if [ -r "$CRED" ]; then
  token="$(CRED="$CRED" python3 -c 'import json,os,sys
try:
    d=json.load(open(os.environ["CRED"]))
    print(d.get("claudeAiOauth",{}).get("accessToken","") or "")
except Exception:
    pass' 2>/dev/null)"
fi

usage_ok=false
resets=""
if [ -n "$token" ]; then
  body="$(curl -fsS --max-time 8 \
    -H "Authorization: Bearer $token" \
    -H "anthropic-beta: oauth-2025-04-20" \
    "$USAGE_URL" 2>/dev/null || true)"
  if [ -n "$body" ]; then
    if resets="$(printf '%s' "$body" | python3 -c 'import json,sys
try:
    d=json.load(sys.stdin)
except Exception:
    sys.exit(3)
w=d.get("five_hour") or {}
print(w.get("resets_at") or "")' 2>/dev/null)"; then
      usage_ok=true
    fi
  fi
fi

need_ping=false
if $usage_ok; then
  if [ -n "$resets" ]; then
    reset_ts="$(date -d "$resets" +%s 2>/dev/null || echo 0)"
    if [ "$reset_ts" -gt "$now" ]; then
      log "janela de 5h ativa (reseta em $(((reset_ts-now)/60))min) — sem ping"
    else
      need_ping=true
    fi
  else
    need_ping=true   # five_hour null/ausente → ociosa
  fi
else
  # /oauth/usage indisponível → fallback por timestamp do último ping.
  last="$(cat "$LAST_FILE" 2>/dev/null || echo 0)"
  case "$last" in ''|*[!0-9]*) last=0 ;; esac
  age=$((now - last))
  if [ "$age" -ge "$FALLBACK_MIN_AGE" ]; then
    log "usage indisponível; fallback: último ping há $((age/60))min — pingando"
    need_ping=true
  else
    log "usage indisponível; fallback: último ping há $((age/60))min (<285min) — sem ping"
  fi
fi

if ! $need_ping; then
  finish 0
fi

# ── Ping mínimo: inicia/renova a janela de 5h ────────────────────────────────
[ -n "$CFG_DIR" ] && export CLAUDE_CONFIG_DIR="$CFG_DIR"
log "janela ociosa — enviando ping de keep-alive (modelo: $PING_MODEL)"
# Diretório neutro: dentro de um projeto o CLI carrega os hooks dele (o
# SessionEnd do vps-manager, p.ex.) — nada disso cabe num ping.
cd "$STATE_DIR" 2>/dev/null || cd /
# stdout E stderr: o "Failed to authenticate" sai no stdout. </dev/null: sem
# ele o CLI espera stdin por 3s e avisa disso.
err="$(timeout "$PING_TIMEOUT" "$CLAUDE_BIN" -p "$PING_PROMPT" --model "$PING_MODEL" </dev/null 2>&1)"
rc=$?
if [ "$rc" -eq 0 ]; then
  echo "$now" > "$LAST_FILE" 2>/dev/null || true
  log "ping ok — janela de 5h religada"
  finish 0
fi
# A saída do CLI vai pro log (última linha, até 300 chars) — antes ia pro
# /dev/null e o job só dizia rc=1.
limpo="$(printf '%s' "$err" | tr -d '\r' | grep -v '^[[:space:]]*$')"
# Prefere a linha que diz o erro; sem ela, a última.
motivo="$(printf '%s\n' "$limpo" | grep -iE 'error|erro|fail|login|logged|auth|expired|denied|limit' | grep -v -i 'hook' | tail -n1)"
[ -z "$motivo" ] && motivo="$(printf '%s\n' "$limpo" | tail -n1)"
motivo="$(printf '%s' "$motivo" | cut -c1-300)"
case "$motivo" in
  *[Ll]ogin*|*[Ll]ogged*|*OAuth*|*authenticate*) motivo="$motivo — $(relogin_hint)" ;;
esac
log "ERRO: ping falhou (rc=$rc): ${motivo:-sem saída}"
exit 1
