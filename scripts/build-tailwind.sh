#!/usr/bin/env bash
# Regenera o CSS estático do Tailwind a partir das classes usadas no frontend.
# RODE ISTO sempre que adicionar/alterar classes Tailwind, senão a classe nova
# não vai existir no CSS embutido (substituímos o CDN runtime por build estático
# para tirar o aviso "cdn.tailwindcss.com should not be used in production").
#
# separa TOOLCHAIN (compartilhada) de CONTEÚDO/SAÍDA (do worktree):
#   • A toolchain (binário tailwindcss 42MB + input.css + config) é IGNORADA no
#     git (.gitignore) e vive SÓ na árvore principal /opt/panel/scripts/.
#     Nenhum worktree tem cópia dela.
#   • O CONTEÚDO escaneado e a SAÍDA gerada são do worktree ALVO, não da principal.
#     Antes, ROOT e o content[] do config eram hardcoded em /opt/panel/...,
#     então um build a partir de um worktree gerava o CSS das features ERRADAS
#     (as da principal) — causa-raiz de "site sem estilo" pós-deploy.
#
# Uso:
#   scripts/build-tailwind.sh [<raiz-alvo>]
#     sem arg  → raiz = repo do cwd (git toplevel) — o worktree em que `make build` roda
#     com arg  → raiz = o caminho passado (ex.: .claude/worktrees/vpsm-22)
set -euo pipefail

# Toolchain compartilhada (não versionada — só existe na árvore principal).
TOOLCHAIN="/opt/panel/scripts"
# Raiz alvo = 1º arg, ou o repo do cwd, ou a principal como último fallback.
TARGET_ROOT="${1:-$(git rev-parse --show-toplevel 2>/dev/null || echo /opt/panel)}"
TARGET_ROOT="$(cd "$TARGET_ROOT" && pwd)"   # normaliza p/ absoluto sem barra final

[[ -x "$TOOLCHAIN/tailwindcss" ]] || {
  echo "✗ toolchain Tailwind ausente: $TOOLCHAIN/tailwindcss" >&2
  echo "  (o binário/config são ignorados no git e vivem só na árvore principal)" >&2
  exit 1
}

# O content[] do config tem caminhos ABSOLUTOS /opt/panel/...; reaponta-os
# pra raiz alvo numa cópia temporária (não muta o config compartilhado).
tmpcfg="$(mktemp)"; trap 'rm -f "$tmpcfg"' EXIT
sed "s#/opt/panel/#$TARGET_ROOT/#g" \
  "$TOOLCHAIN/tailwind/tailwind.config.js" > "$tmpcfg"

"$TOOLCHAIN/tailwindcss" \
  -c "$tmpcfg" \
  -i "$TOOLCHAIN/tailwind/input.css" \
  -o "$TARGET_ROOT/internal/webassets/web/tailwind.css" \
  --minify

out="$TARGET_ROOT/internal/webassets/web/tailwind.css"
echo "OK: $out regenerado ($(stat -c%s "$out") bytes) [scan: $TARGET_ROOT]"
