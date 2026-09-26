#!/usr/bin/env bash
# Exports and verifies the Android/Zig toolchain for building libghostty-vt.
#
# Non-interactive shells (including GitHub Actions steps) do not load
# /etc/profile.d/android-toolchain.sh, so every build invocation must source
# this file explicitly rather than assume the environment is already set up.
# See docs/android-toolchain.md.
#
# Usage: `. ./toolchain-env.sh` from android/terminal-engine/, or
#        `. android/terminal-engine/toolchain-env.sh` from anywhere.
#
# On any missing piece, this exits (or returns, when sourced) with a message
# prefixed `TOOLCHAIN-ENV:` so callers can tell an environment defect apart
# from an actual Zig/Ghostty build failure — the two are budgeted differently
# by the Plan B fallback trigger in 04-PLAN.md.

_toolchain_env_fail() {
  echo "TOOLCHAIN-ENV: $1" >&2
  # Use return when sourced, exit when executed directly.
  if [ -n "${BASH_SOURCE:-}" ] && [ "${BASH_SOURCE[0]}" != "${0}" ]; then
    return 1
  fi
  exit 1
}

_TOOLCHAIN_ENV_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
_TOOLCHAIN_PROPS="$_TOOLCHAIN_ENV_DIR/toolchain.properties"

if [ ! -f "$_TOOLCHAIN_PROPS" ]; then
  _toolchain_env_fail "toolchain.properties not found at $_TOOLCHAIN_PROPS"
  return 1 2>/dev/null || exit 1
fi

# Parse KEY=VALUE lines, ignoring comments and blanks, without sourcing the
# file directly (it deliberately has no shell-executable content).
_toolchain_prop() {
  grep -E "^$1=" "$_TOOLCHAIN_PROPS" | tail -1 | cut -d= -f2-
}

ZIG_VERSION="$(_toolchain_prop ZIG_VERSION)"
NDK_VERSION="$(_toolchain_prop NDK_VERSION)"

if [ -z "$ZIG_VERSION" ]; then
  _toolchain_env_fail "ZIG_VERSION missing from toolchain.properties"
  return 1 2>/dev/null || exit 1
fi
if [ -z "$NDK_VERSION" ]; then
  _toolchain_env_fail "NDK_VERSION missing from toolchain.properties"
  return 1 2>/dev/null || exit 1
fi

export ANDROID_HOME="${ANDROID_HOME:-/opt/android-sdk}"
export ANDROID_SDK_ROOT="${ANDROID_SDK_ROOT:-$ANDROID_HOME}"
export ANDROID_NDK_HOME="${ANDROID_NDK_HOME:-$ANDROID_HOME/ndk/$NDK_VERSION}"
export JAVA_HOME="${JAVA_HOME:-/usr/lib/jvm/java-17-openjdk-amd64}"

_ZIG_DIR="/opt/zig/zig-x86_64-linux-$ZIG_VERSION"

export PATH="$_ZIG_DIR:$JAVA_HOME/bin:$ANDROID_HOME/cmdline-tools/latest/bin:$ANDROID_HOME/platform-tools:$PATH"

# --- Verification: fail loudly and specifically rather than let the build
# --- fail later with a confusing "command not found" or ABI mismatch.

if [ ! -x "$_ZIG_DIR/zig" ]; then
  _toolchain_env_fail "zig $ZIG_VERSION not found at $_ZIG_DIR (checked toolchain.properties ZIG_VERSION=$ZIG_VERSION)"
  return 1 2>/dev/null || exit 1
fi

_ACTUAL_ZIG_VERSION="$("$_ZIG_DIR/zig" version 2>/dev/null || true)"
if [ "$_ACTUAL_ZIG_VERSION" != "$ZIG_VERSION" ]; then
  _toolchain_env_fail "zig at $_ZIG_DIR reports version '$_ACTUAL_ZIG_VERSION', expected '$ZIG_VERSION'"
  return 1 2>/dev/null || exit 1
fi

_NDK_CLANG="$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin/clang"
if [ ! -x "$_NDK_CLANG" ]; then
  _toolchain_env_fail "NDK clang not executable at $_NDK_CLANG (ANDROID_NDK_HOME=$ANDROID_NDK_HOME, NDK_VERSION=$NDK_VERSION)"
  return 1 2>/dev/null || exit 1
fi

if ! command -v javac >/dev/null 2>&1; then
  _toolchain_env_fail "javac not found on PATH after exporting JAVA_HOME=$JAVA_HOME"
  return 1 2>/dev/null || exit 1
fi

_JAVAC_VERSION="$(javac -version 2>&1)"
case "$_JAVAC_VERSION" in
  *" 17."*|*" 17"|*"javac 17"*)
    ;;
  *)
    _toolchain_env_fail "javac -version reports '$_JAVAC_VERSION', expected 17 (JAVA_HOME=$JAVA_HOME)"
    return 1 2>/dev/null || exit 1
    ;;
esac

echo "toolchain-env: zig $ZIG_VERSION, NDK $NDK_VERSION, javac 17 — OK" >&2
