#!/usr/bin/env bash
# Regenerates the static Tailwind CSS from the classes used in the frontend.
# Run it whenever you add or change Tailwind classes, otherwise the new class
# will not exist in the embedded CSS.
#
# The TOOLCHAIN is shared, the CONTENT and OUTPUT belong to the target worktree:
#   - The toolchain (tailwindcss binary + input.css + config) is git-ignored and
#     lives only in the main tree, /opt/panel/scripts/.
#   - The scanned content and the generated output come from the TARGET tree, so a
#     build from a worktree never ships the CSS of another branch's features.
#
# Usage:
#   scripts/build-tailwind.sh [<target-root>]
#     no arg   -> root = the cwd's repo (git toplevel), where `make build` runs
#     with arg -> root = the given path (e.g. .claude/worktrees/panel-22)
set -euo pipefail

# Shared toolchain (not versioned, it only exists in the main tree).
TOOLCHAIN="/opt/panel/scripts"
# Target root = 1st arg, or the cwd's repo, or the main tree as a last fallback.
TARGET_ROOT="${1:-$(git rev-parse --show-toplevel 2>/dev/null || echo /opt/panel)}"
TARGET_ROOT="$(cd "$TARGET_ROOT" && pwd)"   # absolute, no trailing slash

[[ -x "$TOOLCHAIN/tailwindcss" ]] || {
  echo "✗ Tailwind toolchain missing: $TOOLCHAIN/tailwindcss" >&2
  echo "  (the binary and config are git-ignored and live only in the main tree)" >&2
  exit 1
}

# The config's content[] has ABSOLUTE /opt/panel/... paths; point them at the
# target root in a temporary copy (the shared config is never mutated).
tmpcfg="$(mktemp)"; trap 'rm -f "$tmpcfg"' EXIT
sed "s#/opt/panel/#$TARGET_ROOT/#g" \
  "$TOOLCHAIN/tailwind/tailwind.config.js" > "$tmpcfg"

"$TOOLCHAIN/tailwindcss" \
  -c "$tmpcfg" \
  -i "$TOOLCHAIN/tailwind/input.css" \
  -o "$TARGET_ROOT/internal/webassets/web/tailwind.css" \
  --minify

out="$TARGET_ROOT/internal/webassets/web/tailwind.css"
echo "OK: $out regenerated ($(stat -c%s "$out") bytes) [scan: $TARGET_ROOT]"
