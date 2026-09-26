#!/usr/bin/env bash
# Exports and checks the toolchain build-libghostty.sh needs: Zig, the Android
# NDK and JDK 17. Sourced, not executed.
#
# Zig is taken from $ZIG when set, else from PATH if it is the pinned version,
# else downloaded once into .build/zig and checked against the sha256 in
# toolchain.properties. The NDK is looked up under $ANDROID_HOME and installed
# with sdkmanager when missing.
#
# Every failure is prefixed `TOOLCHAIN-ENV:` so a missing tool is easy to tell
# apart from a Zig or Ghostty build failure.

_toolchain_env_fail() {
  echo "TOOLCHAIN-ENV: $1" >&2
  return 1
}

_toolchain_env() {
  local dir props zig_version ndk_version arch sha zig_dir tarball
  dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  props="$dir/toolchain.properties"
  [ -f "$props" ] || { _toolchain_env_fail "toolchain.properties not found at $props"; return 1; }
  _prop() { grep -E "^$1=" "$props" | tail -1 | cut -d= -f2-; }

  zig_version="$(_prop ZIG_VERSION)"
  ndk_version="$(_prop NDK_VERSION)"
  [ -n "$zig_version" ] && [ -n "$ndk_version" ] ||
    { _toolchain_env_fail "ZIG_VERSION or NDK_VERSION missing from toolchain.properties"; return 1; }

  if [ -z "${ANDROID_HOME:-}" ] && [ -f "$dir/../local.properties" ]; then
    ANDROID_HOME="$(grep -E '^sdk.dir=' "$dir/../local.properties" | cut -d= -f2-)"
  fi
  [ -n "${ANDROID_HOME:-}" ] && [ -d "$ANDROID_HOME" ] ||
    { _toolchain_env_fail "set ANDROID_HOME (or sdk.dir in android/local.properties) to your Android SDK"; return 1; }
  export ANDROID_HOME ANDROID_SDK_ROOT="${ANDROID_SDK_ROOT:-$ANDROID_HOME}"
  export ANDROID_NDK_HOME="$ANDROID_HOME/ndk/$ndk_version"
  export ANDROID_NDK_ROOT="$ANDROID_NDK_HOME"

  if [ -n "${JAVA_HOME:-}" ]; then
    export PATH="$JAVA_HOME/bin:$PATH"
  fi

  if [ ! -x "$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin/clang" ]; then
    local sdkmanager="$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager"
    [ -x "$sdkmanager" ] ||
      { _toolchain_env_fail "NDK $ndk_version is missing and sdkmanager was not found at $sdkmanager"; return 1; }
    echo "toolchain-env: installing NDK $ndk_version with sdkmanager" >&2
    (yes || true) | "$sdkmanager" --install "ndk;$ndk_version" >/dev/null ||
      { _toolchain_env_fail "sdkmanager could not install ndk;$ndk_version"; return 1; }
  fi

  if [ -n "${ZIG:-}" ]; then
    zig_dir="$(dirname "$ZIG")"
  elif command -v zig >/dev/null 2>&1 && [ "$(zig version 2>/dev/null)" = "$zig_version" ]; then
    zig_dir="$(dirname "$(command -v zig)")"
  else
    case "$(uname -m)" in
      x86_64) arch=x86_64; sha="$(_prop ZIG_SHA256_X86_64_LINUX)" ;;
      aarch64) arch=aarch64; sha="$(_prop ZIG_SHA256_AARCH64_LINUX)" ;;
      *) _toolchain_env_fail "no pinned Zig download for $(uname -m); install Zig $zig_version and set ZIG"; return 1 ;;
    esac
    zig_dir="$dir/.build/zig-$zig_version"
    if [ ! -x "$zig_dir/zig" ]; then
      tarball="$dir/.build/zig-$zig_version.tar.xz"
      mkdir -p "$zig_dir"
      echo "toolchain-env: downloading Zig $zig_version" >&2
      curl -fsSL -o "$tarball" "https://ziglang.org/download/$zig_version/zig-$arch-linux-$zig_version.tar.xz" ||
        { _toolchain_env_fail "Zig download failed"; return 1; }
      echo "$sha  $tarball" | sha256sum -c --quiet - ||
        { rm -f "$tarball"; _toolchain_env_fail "Zig tarball does not match the pinned sha256"; return 1; }
      tar -xJf "$tarball" -C "$zig_dir" --strip-components=1 && rm -f "$tarball"
    fi
  fi
  export PATH="$zig_dir:$PATH"

  [ "$(zig version 2>/dev/null)" = "$zig_version" ] ||
    { _toolchain_env_fail "zig in $zig_dir is not version $zig_version"; return 1; }
  javac -version 2>&1 | grep -q ' 17' ||
    { _toolchain_env_fail "JDK 17 is required (javac -version: $(javac -version 2>&1)); set JAVA_HOME"; return 1; }

  echo "toolchain-env: zig $zig_version, NDK $ndk_version, JDK 17" >&2
}

_toolchain_env
