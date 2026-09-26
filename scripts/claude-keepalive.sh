#!/usr/bin/env bash
# claude-keepalive: keeps the Max plan's 5h usage window "always on".
#
# The 5h window is server-side state: it starts on the first message to a model
# and resets 5h later; with no activity after the reset the counter stays idle.
# Run hourly by the scheduler (one job per account), this only spends a ping WHEN
# the window is idle, so the hourly run is almost always a cheap no-op and restarts
# the counter within 1h of any reset.
#
# Usage:  claude-keepalive <jordan|sam>
#
# Gate:
#   1) read the account's accessToken and query GET /api/oauth/usage (read-only);
#   2) five_hour.resets_at in the future -> window ACTIVE -> no ping;
#      missing/null/past                 -> window IDLE   -> ping;
#   3) query failed (network/429)        -> ping only if the last ping is older
#      than 4h45m (state file).
# Querying /oauth/usage does NOT start the counter; a minimal `claude -p` on the
# cheapest model does.
#
# Security: the token comes from the account's .credentials.json (same trust
# boundary as ratelimits.go) and is NEVER logged.
#
# The refreshToken has a FIXED expiry (refreshTokenExpiresAt); once it passes the
# CLI wipes the credential and the account stays logged out until a browser login.
# So: a wiped/expired credential is an error naming the fix, and with < 48h left
# the job FAILS once every 12h with a warning (a failed job is the notification
# channel that reaches the user).

set -uo pipefail

USAGE_URL="https://api.anthropic.com/api/oauth/usage"
STATE_DIR="${PANEL_KEEPALIVE_DIR:-/opt/panel/data/keepalive}"
PING_PROMPT="reply only: ok"
PING_MODEL="${PANEL_KEEPALIVE_MODEL:-haiku}"
FALLBACK_MIN_AGE=17100   # 4h45m in seconds: minimum age to ping in the fallback
PING_TIMEOUT=120

log() { printf '%s [keepalive:%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "${ACCT:-?}" "$*"; }

ACCT="${1:-}"
if [ -z "$ACCT" ]; then
  echo "usage: claude-keepalive <jordan|sam>" >&2
  exit 2
fi

# Mirrors claudeacct.defaultRegistry: jordan uses the default config ($HOME/.claude),
# sam uses a dedicated CLAUDE_CONFIG_DIR.
export HOME=/root
CFG_DIR=""
# Never inherit the account from the environment: run from a sam session it would
# ping the wrong account and pass.
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
    log "unknown account: $ACCT (use jordan|sam)"
    exit 2
    ;;
esac

# Resolve the claude binary (the daemon's PATH may lack ~/.local/bin).
CLAUDE_BIN="$(command -v claude 2>/dev/null || true)"
[ -z "$CLAUDE_BIN" ] && [ -x /root/.local/bin/claude ] && CLAUDE_BIN=/root/.local/bin/claude
if [ -z "$CLAUDE_BIN" ]; then
  log "ERROR: 'claude' binary not found"
  exit 1
fi

mkdir -p "$STATE_DIR" 2>/dev/null || true
LAST_FILE="$STATE_DIR/last-$ACCT"
WARN_FILE="$STATE_DIR/login-warn-$ACCT"
now="$(date +%s)"
WARN_WINDOW=172800   # 48h: how early to warn about an expiring login
WARN_EVERY=43200     # 12h: minimum interval between warnings

relogin_hint() {
  if [ -n "$CFG_DIR" ]; then
    echo "log in again: CLAUDE_CONFIG_DIR=$CFG_DIR claude auth login --claudeai"
  else
    echo "log in again: claude auth login --claudeai (HOME=/root)"
  fi
}

# Login state (reads no secret: only presence and dates).
login_state="$(CRED="$CRED" python3 -c 'import json,os,time
try:
    o=json.load(open(os.environ["CRED"])).get("claudeAiOauth") or {}
except Exception:
    print("no-credential 0"); raise SystemExit
if not o.get("refreshToken"):
    print("logged-out 0"); raise SystemExit
exp=int((o.get("refreshTokenExpiresAt") or 0)/1000)
print("ok", exp)' 2>/dev/null || echo "no-credential 0")"
login_status="${login_state%% *}"
login_exp="${login_state##* }"
case "$login_status" in
  no-credential)
    log "ERROR: account credential missing or unreadable ($CRED) — $(relogin_hint)"
    exit 1 ;;
  logged-out)
    log "ERROR: account logged out — the login expired and the CLI wiped the credential; $(relogin_hint)"
    exit 1 ;;
esac
login_warn=""
if [ "${login_exp:-0}" -gt 0 ]; then
  left=$((login_exp - now))
  if [ "$left" -le 0 ]; then
    log "ERROR: the account login expired on $(date -d "@$login_exp" '+%Y-%m-%d %H:%M'); $(relogin_hint)"
    exit 1
  elif [ "$left" -le "$WARN_WINDOW" ]; then
    last_warn="$(cat "$WARN_FILE" 2>/dev/null || echo 0)"
    case "$last_warn" in ''|*[!0-9]*) last_warn=0 ;; esac
    if [ $((now - last_warn)) -ge "$WARN_EVERY" ]; then
      login_warn="WARNING: the account login expires in $((left/3600))h ($(date -d "@$login_exp" '+%Y-%m-%d %H:%M')), then it logs out; $(relogin_hint)"
    fi
  fi
fi
# The warning is emitted at the END (after the ping) so it never blocks today's keep-alive.
finish() {
  if [ -n "$login_warn" ]; then
    log "$login_warn"
    echo "$now" > "$WARN_FILE" 2>/dev/null || true
    exit 1
  fi
  exit "${1:-0}"
}

# Gate: is the 5h window idle?
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
      log "5h window active (resets in $(((reset_ts-now)/60))min) — no ping"
    else
      need_ping=true
    fi
  else
    need_ping=true   # five_hour null/missing -> idle
  fi
else
  # /oauth/usage unavailable -> fall back to the last ping's timestamp.
  last="$(cat "$LAST_FILE" 2>/dev/null || echo 0)"
  case "$last" in ''|*[!0-9]*) last=0 ;; esac
  age=$((now - last))
  if [ "$age" -ge "$FALLBACK_MIN_AGE" ]; then
    log "usage unavailable; fallback: last ping $((age/60))min ago — pinging"
    need_ping=true
  else
    log "usage unavailable; fallback: last ping $((age/60))min ago (<285min) — no ping"
  fi
fi

if ! $need_ping; then
  finish 0
fi

# Minimal ping: starts/renews the 5h window.
[ -n "$CFG_DIR" ] && export CLAUDE_CONFIG_DIR="$CFG_DIR"
log "window idle — sending keep-alive ping (model: $PING_MODEL)"
# Neutral directory: inside a project the CLI would load that project's hooks.
cd "$STATE_DIR" 2>/dev/null || cd /
# Capture stdout AND stderr ("Failed to authenticate" goes to stdout). </dev/null
# stops the CLI from waiting 3s on stdin.
err="$(timeout "$PING_TIMEOUT" "$CLAUDE_BIN" -p "$PING_PROMPT" --model "$PING_MODEL" </dev/null 2>&1)"
rc=$?
if [ "$rc" -eq 0 ]; then
  echo "$now" > "$LAST_FILE" 2>/dev/null || true
  log "ping ok — 5h window restarted"
  finish 0
fi
# Log the CLI's output (one line, up to 300 chars); it never contains the token.
cleaned="$(printf '%s' "$err" | tr -d '\r' | grep -v '^[[:space:]]*$')"
# Prefer the line that states the error; otherwise the last one.
reason="$(printf '%s\n' "$cleaned" | grep -iE 'error|fail|login|logged|auth|expired|denied|limit' | grep -v -i 'hook' | tail -n1)"
[ -z "$reason" ] && reason="$(printf '%s\n' "$cleaned" | tail -n1)"
reason="$(printf '%s' "$reason" | cut -c1-300)"
case "$reason" in
  *[Ll]ogin*|*[Ll]ogged*|*OAuth*|*authenticate*) reason="$reason — $(relogin_hint)" ;;
esac
log "ERROR: ping failed (rc=$rc): ${reason:-no output}"
exit 1
