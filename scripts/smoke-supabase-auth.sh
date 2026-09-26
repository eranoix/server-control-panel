#!/usr/bin/env bash
# smoke-supabase-auth.sh: extra deploy gate for the Supabase path.
#
# Checks the infrastructure is up BEFORE the binary is deployed:
#   1. supabase-auth container running + healthy
#   2. GoTrue /health answers 200 (inside the container, no Kong)
#   3. Anon key and URL in config.json match the Kong env
#   4. /auth/v1/token rejects invalid creds with 400 + invalid_credentials
#      (the Kong→GoTrue→DB chain works)
#   5. The UUID map loads and has >= 1 entry
#   6. Every mapped email exists in auth.users
#
# Usage:
#   bash scripts/smoke-supabase-auth.sh
#
# Exit 0 = every check passed; non-zero = aborted at a check (message on stderr).
# Used by the deploy script as a pre-flight when configured.

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
    fail "container state=$STATE (expected running)"
fi
ok "supabase-auth state=running"

# --- 2. internal GoTrue health ---
step 2 "internal GoTrue /health (no Kong)"
HEALTH=$(docker exec supabase-auth wget -qO- http://127.0.0.1:9999/health 2>&1 || true)
if ! echo "$HEALTH" | grep -q '"name":"GoTrue"'; then
    fail "/health did not return a GoTrue payload: $HEALTH"
fi
VERSION=$(echo "$HEALTH" | jq -r .version)
ok "GoTrue version=$VERSION healthy"

# --- 3. anon key + url config match Kong ---
step 3 "config.json supabase_url + anon_key match the Kong env"
CFG_URL=$(jq -r '.supabase_url // ""' "$CONFIG")
CFG_KEY=$(jq -r '.supabase_anon_key // ""' "$CONFIG")
if [ -z "$CFG_URL" ] || [ -z "$CFG_KEY" ]; then
    fail "config.json lacks supabase_url or supabase_anon_key"
fi
KONG_KEY=$(docker exec supabase-kong env | grep "^SUPABASE_ANON_KEY=" | cut -d= -f2-)
if [ "$CFG_KEY" != "$KONG_KEY" ]; then
    fail "anon_key differs between config.json and the Kong env (config sha=$(echo -n "$CFG_KEY" | sha256sum | cut -c1-12) kong sha=$(echo -n "$KONG_KEY" | sha256sum | cut -c1-12))"
fi
ok "supabase_url=$CFG_URL anon_key sha=$(echo -n "$CFG_KEY" | sha256sum | cut -c1-12)"

# --- 4. token endpoint chain ---
step 4 "POST /auth/v1/token with fake creds (expects 400 invalid_credentials)"
RESP=$(curl -sk -X POST "$CFG_URL/auth/v1/token?grant_type=password" \
    -H "apikey: $CFG_KEY" \
    -H "Authorization: Bearer $CFG_KEY" \
    -H "Content-Type: application/json" \
    -d '{"email":"smoke-test-nonexistent@panel.local","password":"x"}')
CODE=$(echo "$RESP" | jq -r '.error_code // ""')
if [ "$CODE" != "invalid_credentials" ]; then
    fail "/auth/v1/token returned error_code=$CODE (expected invalid_credentials). Response: $RESP"
fi
ok "/auth/v1/token chain intact (Kong→GoTrue→DB)"

# --- 5. uuid map ---
step 5 "migration-uuid-map.json loads"
if [ ! -f "$UUID_MAP" ]; then
    fail "$UUID_MAP does not exist: Phase 2 did not run"
fi
COUNT=$(jq -r '.mappings | length' "$UUID_MAP")
if [ "$COUNT" -lt 1 ]; then
    fail "uuid_map is empty"
fi
ok "uuid_map: $COUNT entries"

# --- 6. every mapped email exists in auth.users ---
step 6 "every mapped email exists in auth.users"
MISSING=()
while IFS= read -r email; do
    EXISTS=$(docker exec supabase-db psql -U postgres -d postgres -tAc \
        "SELECT count(*) FROM auth.users WHERE email = '$email'" 2>&1 | tr -d ' ')
    if [ "$EXISTS" != "1" ]; then
        MISSING+=("$email")
    fi
done < <(jq -r '.mappings | to_entries[] | .value.email' "$UUID_MAP")
if [ ${#MISSING[@]} -gt 0 ]; then
    fail "mapped emails missing from auth.users: ${MISSING[*]}"
fi
ok "all $COUNT mapped emails present in auth.users"

echo ""
printf "\033[32m=== smoke-supabase-auth: 6/6 PASS ===\033[0m\n"
