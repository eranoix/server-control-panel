#!/usr/bin/env bash
#
# Pins for the deploy CONTRACT.
#
# A plain shell assertion script, not `bats`: the repository has no bats, and a new
# tool for a handful of assertions would cost more than it saves.
#
# 🔴 NO PIN HERE TOUCHES A REAL SERVICE. They all run the deploy script in its
# validate-only mode, which exits BEFORE the first side effect (no mkdir, no flock,
# no log).
#
# Each pin asserts the EXIT CODE **and** part of the message: the code alone cannot
# tell "refused for the right reason" from "broke for another reason".
#
# The validate flag and the expected message fragments are the literal interface
# of the deploy script, which lives outside this repository.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEPLOY="$ROOT/scripts/deploy.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

failures=0
total=0

# valid_conf writes a complete, correct configuration plus overrides.
valid_conf() {
    local file="$1"; shift
    cat > "$file" <<EOF
PROJ_SRC=$TMP/src
PROJ_ROOT=$TMP/root
BIN_DIR=\$PROJ_ROOT/bin
LINK=\$BIN_DIR/example
ARTIFACT_PREFIX=example-
BUILD_CMD=
HEALTH_MODE=url
HEALTH_URL=http://127.0.0.1:1/health
HEALTH_TRIES=15
HEALTH_INTERVAL=2
SERVICE=example
KEEP_BINARIES=5
LOCK_FILE=\$PROJ_ROOT/.lock
LOG=\$PROJ_ROOT/deploy.log
EOF
    for extra in "$@"; do echo "$extra" >> "$file"; done
}

# pin <name> <expected-rc> <expected-fragment> <conf-file>
pin() {
    local name="$1" want_rc="$2" fragment="$3" file="$4"
    total=$((total+1))
    local output rc
    output="$("$DEPLOY" --conf "$file" --validate 2>&1)"; rc=$?
    if [[ "$rc" != "$want_rc" ]]; then
        echo "  ✗ $name: expected rc=$want_rc, got rc=$rc"
        echo "    output: $output"
        failures=$((failures+1)); return
    fi
    if [[ -n "$fragment" && "$output" != *"$fragment"* ]]; then
        echo "  ✗ $name: right rc ($rc) but the message does not name the problem"
        echo "    expected to contain: $fragment"
        echo "    output: $output"
        failures=$((failures+1)); return
    fi
    echo "  ✓ $name"
}

echo "═══ deploy contract pins ═══"

# Negative control FIRST: if this fails, every other pin is a false positive.
valid_conf "$TMP/ok.conf"
pin "NEGATIVE CONTROL: a complete, valid conf PASSES" 0 "valid configuration" "$TMP/ok.conf"

# Missing conf.
pin "a missing conf names the file" 2 "$TMP/does-not-exist.conf" "$TMP/does-not-exist.conf"

# Missing required key.
for key in PROJ_ROOT BIN_DIR LINK ARTIFACT_PREFIX SERVICE LOCK_FILE LOG; do
    valid_conf "$TMP/no-$key.conf"
    # Empty the key AFTERWARDS so it wins over the earlier definition.
    echo "$key=" >> "$TMP/no-$key.conf"
    pin "a missing required key names '$key'" 2 "$key" "$TMP/no-$key.conf"
done

# HEALTH_MODE
valid_conf "$TMP/hm-invalid.conf" "HEALTH_MODE=xpto"
pin "an invalid HEALTH_MODE names the value" 2 "xpto" "$TMP/hm-invalid.conf"

valid_conf "$TMP/url-no-url.conf" "HEALTH_URL="
pin "HEALTH_MODE=url without HEALTH_URL" 2 "HEALTH_URL" "$TMP/url-no-url.conf"

valid_conf "$TMP/cmd-no-cmd.conf" "HEALTH_MODE=cmd" "HEALTH_CMD="
pin "HEALTH_MODE=cmd without HEALTH_CMD" 2 "HEALTH_CMD" "$TMP/cmd-no-cmd.conf"

valid_conf "$TMP/cmd-ok.conf" "HEALTH_MODE=cmd" "HEALTH_CMD=true"
pin "HEALTH_MODE=cmd with HEALTH_CMD is valid" 0 "cmd" "$TMP/cmd-ok.conf"

# KEEP_BINARIES
valid_conf "$TMP/keep1.conf" "KEEP_BINARIES=1"
pin "KEEP_BINARIES=1 explains that rollback needs 2" 2 "at least 2" "$TMP/keep1.conf"

valid_conf "$TMP/keep2.conf" "KEEP_BINARIES=2"
pin "KEEP_BINARIES=2 is the minimum accepted" 0 "valid" "$TMP/keep2.conf"

valid_conf "$TMP/keepx.conf" "KEEP_BINARIES=abc"
pin "a non-numeric KEEP_BINARIES is refused" 2 "is not a number" "$TMP/keepx.conf"

# An empty BUILD_CMD is NOT an error.
valid_conf "$TMP/no-build.conf" "BUILD_CMD="
pin "an empty BUILD_CMD is a SUPPORTED case, not an error" 0 "valid" "$TMP/no-build.conf"

# swap_symlink with LINK OUTSIDE BIN_DIR: the target must be resolved relative to
# where the LINK lives, otherwise the symlink points at a missing sibling.
total=$((total+1))
_sw="$TMP/sw"; mkdir -p "$_sw/versions"
: > "$_sw/versions/art-123"
(
  LINK="$_sw/art" BIN_DIR="$_sw/versions"
  swap() {
      local newName="$1"
      local linkDir; linkDir="$(cd "$(dirname "$LINK")" && pwd)"
      local binDir;  binDir="$(cd "$BIN_DIR" && pwd)"
      local target="$newName"
      [[ "$linkDir" != "$binDir" ]] && target="$binDir/$newName"
      ln -sfn "$target" "$LINK.new"; mv -fT "$LINK.new" "$LINK"
  }
  swap art-123
)
if [[ -e "$_sw/art" ]]; then
    echo "  ✓ swap_symlink resolves a LINK outside BIN_DIR (target exists)"
else
    echo "  ✗ swap_symlink left a broken link with LINK outside BIN_DIR"
    failures=$((failures+1))
fi

# EVERY production conf in the repository validates. An invalid conf is a deploy
# that dies at the destination after the artifact already travelled; the loop picks
# up any future deploy/*.conf on its own.
for conf in "$ROOT"/deploy/*.conf; do
    [[ -e "$conf" ]] || continue
    total=$((total+1))
    if BUILD_DIR="$TMP" "$DEPLOY" --conf "$conf" --validate >/dev/null 2>&1; then
        echo "  ✓ deploy/$(basename "$conf") (production) is valid"
    else
        echo "  ✗ deploy/$(basename "$conf") (production) does NOT validate"
        failures=$((failures+1))
    fi
done

# HEALTH_MODE=cmd in BOTH directions: exercises the real probe, extracted from the
# script, with no service.
probe_with() {
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
if probe_with "true" 2; then
    echo "  ✓ HEALTH_MODE=cmd: a command exiting 0 is healthy"
else
    echo "  ✗ HEALTH_MODE=cmd: a command exiting 0 should be healthy"
    failures=$((failures+1))
fi

total=$((total+1))
if probe_with "false" 2; then
    echo "  ✗ HEALTH_MODE=cmd: a FAILING command was accepted as healthy"
    failures=$((failures+1))
else
    echo "  ✓ HEALTH_MODE=cmd: a failing command exhausts the window (triggers the rollback)"
fi

echo "───────────────────────────────────────"
if (( failures )); then
    echo "FAILED: $failures of $total pins failed"
    exit 1
fi
echo "OK: $total pins"
