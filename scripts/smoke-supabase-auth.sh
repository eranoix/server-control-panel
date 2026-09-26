#!/usr/bin/env bash
# smoke-supabase-auth.sh — gate adicional de deploy para o caminho Supabase.
#
# Verifica que a infra está em pé ANTES do deploy do binário:
#   1. supabase-auth container running + healthy
#   2. GoTrue /health responde 200 (via container interno, sem Kong)
#   3. Anon key e URL no config.json batem com Kong env
#   4. /auth/v1/token rejeita creds inválidas com 400 + invalid_credentials
#      (= chain Kong→GoTrue→DB funcionando)
#   5. UUID map carrega e tem >= 1 entry
#   6. Cada email mapeado existe em auth.users (verifica drift desde a Phase 2)
#
# Uso:
#   bash scripts/smoke-supabase-auth.sh
#
# Exit code 0 = todos os checks PASS. != 0 = abortou em algum check (msg
# direta no stderr). Usado pelo deploy.sh como pre-flight quando configurado.

set -euo pipefail

ROOT="/opt/panel"
CONFIG="$ROOT/data/config.json"
UUID_MAP="$ROOT/data/migration-uuid-map.json"

ok() { printf "\033[32mOK:\033[0m   %s\n" "$1"; }
fail() { printf "\033[31mFAIL:\033[0m %s\n" "$1" >&2; exit 1; }
step() { printf "\033[33m[%d] %s\033[0m\n" "$1" "$2"; }

# --- 1. container running ---
step 1 "container supabase-auth state=running"
STATE=$(docker inspect --format '{{.State.Status}}' supabase-auth 2>/dev/null || echo missing)
if [ "$STATE" != "running" ]; then
    fail "container state=$STATE (esperado running)"
fi
ok "supabase-auth state=running"

# --- 2. GoTrue health interno ---
step 2 "GoTrue /health interno (sem Kong)"
HEALTH=$(docker exec supabase-auth wget -qO- http://127.0.0.1:9999/health 2>&1 || true)
if ! echo "$HEALTH" | grep -q '"name":"GoTrue"'; then
    fail "/health não retornou GoTrue payload: $HEALTH"
fi
VERSION=$(echo "$HEALTH" | jq -r .version)
ok "GoTrue version=$VERSION healthy"

# --- 3. anon key + url config match Kong ---
step 3 "config.json supabase_url + anon_key bate com Kong env"
CFG_URL=$(jq -r '.supabase_url // ""' "$CONFIG")
CFG_KEY=$(jq -r '.supabase_anon_key // ""' "$CONFIG")
if [ -z "$CFG_URL" ] || [ -z "$CFG_KEY" ]; then
    fail "config.json sem supabase_url ou supabase_anon_key"
fi
KONG_KEY=$(docker exec supabase-kong env | grep "^SUPABASE_ANON_KEY=" | cut -d= -f2-)
if [ "$CFG_KEY" != "$KONG_KEY" ]; then
    fail "anon_key divergente entre config.json e Kong env (config sha=$(echo -n "$CFG_KEY" | sha256sum | cut -c1-12) kong sha=$(echo -n "$KONG_KEY" | sha256sum | cut -c1-12))"
fi
ok "supabase_url=$CFG_URL anon_key sha=$(echo -n "$CFG_KEY" | sha256sum | cut -c1-12)"

# --- 4. token endpoint chain ---
step 4 "POST /auth/v1/token com creds fake (espera 400 invalid_credentials)"
RESP=$(curl -sk -X POST "$CFG_URL/auth/v1/token?grant_type=password" \
    -H "apikey: $CFG_KEY" \
    -H "Authorization: Bearer $CFG_KEY" \
    -H "Content-Type: application/json" \
    -d '{"email":"smoke-test-nonexistent@vpsm.local","password":"x"}')
CODE=$(echo "$RESP" | jq -r '.error_code // ""')
if [ "$CODE" != "invalid_credentials" ]; then
    fail "/auth/v1/token retornou error_code=$CODE (esperava invalid_credentials). Resposta: $RESP"
fi
ok "/auth/v1/token chain integra (Kong→GoTrue→DB)"

# --- 5. uuid map ---
step 5 "migration-uuid-map.json carrega"
if [ ! -f "$UUID_MAP" ]; then
    fail "$UUID_MAP não existe — Phase 2 não rodou"
fi
COUNT=$(jq -r '.mappings | length' "$UUID_MAP")
if [ "$COUNT" -lt 1 ]; then
    fail "uuid_map vazio"
fi
ok "uuid_map: $COUNT entries"

# --- 6. cada email mapeado existe em auth.users ---
step 6 "cada email mapeado existe em auth.users"
MISSING=()
while IFS= read -r email; do
    EXISTS=$(docker exec supabase-db psql -U postgres -d postgres -tAc \
        "SELECT count(*) FROM auth.users WHERE email = '$email'" 2>&1 | tr -d ' ')
    if [ "$EXISTS" != "1" ]; then
        MISSING+=("$email")
    fi
done < <(jq -r '.mappings | to_entries[] | .value.email' "$UUID_MAP")
if [ ${#MISSING[@]} -gt 0 ]; then
    fail "emails mapeados não existem em auth.users: ${MISSING[*]}"
fi
ok "todos os $COUNT emails mapeados presentes em auth.users"

echo ""
printf "\033[32m=== smoke-supabase-auth: 6/6 PASS ===\033[0m\n"
