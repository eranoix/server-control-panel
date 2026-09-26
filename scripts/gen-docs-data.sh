#!/usr/bin/env bash
#
# gen-docs-data.sh: regenerates the repo-derived blocks inside the HTML technical
# report so it cannot drift. Replaces ONLY the content between the markers
# <!-- GEN:name --> ... <!-- /GEN:name -->; curated text is preserved.
#
# Generated blocks:
#   GEN:metrics     metric cards (LOC, packages, routes, handlers, ...)
#   GEN:locbars     LOC bars per subsystem (top 10)
#   GEN:routes      full route inventory (path + file:line)
#   GEN:routecount  number in the topnav badge (inline)
#
# Usage:  scripts/gen-docs-data.sh   (idempotent)

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
DOC=".docs/Technical Documentation - Server Control Panel.html"

[ -f "$DOC" ] || { echo "✗ $DOC does not exist" >&2; exit 1; }

# ---------- metrics (measured from the repo) ----------
LOC=$(find internal cmd -name '*.go' 2>/dev/null | xargs wc -l 2>/dev/null | tail -1 | awk '{print $1}')
PKGS=$(ls -d internal/*/ 2>/dev/null | wc -l | tr -d ' ')
HANDLERS=$(ls internal/api/handlers_*.go 2>/dev/null | wc -l | tr -d ' ')
ROUTES=$(grep -rhoE 'HandleFunc\("[^"]+"' internal/api/*.go 2>/dev/null | sort -u | wc -l | tr -d ' ')
SUBCMDS=$(grep -rhoE 'case "[a-z][a-z-]+"' cmd/panelctl/*.go 2>/dev/null | sort -u | wc -l | tr -d ' ')

# ---------- helper: replaces the content between markers ----------
# replace_block NAME FRAGMENT_FILE
replace_block() {
  local name="$1" frag="$2" tmp
  tmp="$(mktemp)"
  awk -v openm="<!-- GEN:${name} -->" -v closem="<!-- /GEN:${name} -->" -v fragfile="$frag" '
    index($0, openm)  { print; while ((getline line < fragfile) > 0) print line; close(fragfile); skip=1; next }
    index($0, closem) { skip=0 }
    !skip
  ' "$DOC" > "$tmp"
  mv "$tmp" "$DOC"
}

# ---------- GEN:metrics ----------
METRICS_FRAG="$(mktemp)"
cat > "$METRICS_FRAG" <<EOF
<div class="grid4">
  <div class="card metric"><div class="value">${LOC}</div><div class="label">LOC (internal + cmd)</div><div class="metric-bar"><div class="metric-bar-fill" style="width:100%"></div></div></div>
  <div class="card metric"><div class="value">${PKGS}</div><div class="label">Packages in internal/</div><div class="metric-bar"><div class="metric-bar-fill" style="width:85%"></div></div></div>
  <div class="card metric"><div class="value">${ROUTES}</div><div class="label">HTTP/WS routes</div><div class="metric-bar"><div class="metric-bar-fill" style="width:100%"></div></div></div>
  <div class="card metric"><div class="value">${HANDLERS}</div><div class="label">Handler files</div><div class="metric-bar"><div class="metric-bar-fill" style="width:65%"></div></div></div>
  <div class="card metric"><div class="value">2</div><div class="label">Binaries (server + ctl)</div><div class="metric-bar"><div class="metric-bar-fill" style="width:20%"></div></div></div>
  <div class="card metric"><div class="value">${SUBCMDS}</div><div class="label">panelctl subcommands</div><div class="metric-bar"><div class="metric-bar-fill" style="width:80%"></div></div></div>
</div>
EOF
replace_block metrics "$METRICS_FRAG"

# ---------- GEN:locbars (top 10 by LOC) ----------
LOCBARS_FRAG="$(mktemp)"
{
  echo '<div style="display:flex;flex-direction:column;gap:6px;font-size:12px">'
  # largest value (for the scale)
  MAX=$(for d in internal/*/; do find "$d" -name '*.go' 2>/dev/null | xargs cat 2>/dev/null | wc -l; done | sort -rn | head -1)
  [ "${MAX:-0}" -gt 0 ] || MAX=1
  for d in internal/*/; do
    n=$(find "$d" -name '*.go' 2>/dev/null | xargs cat 2>/dev/null | wc -l | tr -d ' ')
    echo "$n ${d%/}"
  done | sort -rn | head -10 | while read -r n pkg; do
    pct=$(( n * 100 / MAX ))
    printf '  <div style="display:flex;align-items:center;gap:10px"><code style="min-width:140px">%s</code><div class="metric-bar" style="flex:1;height:10px;margin:0"><div class="metric-bar-fill" style="width:%s%%"></div></div><span class="muted" style="min-width:52px;text-align:right">%s</span></div>\n' "$pkg" "$pct" "$n"
  done
  echo '</div>'
} > "$LOCBARS_FRAG"
replace_block locbars "$LOCBARS_FRAG"

# ---------- GEN:routes (inventory: path + file:line) ----------
ROUTES_FRAG="$(mktemp)"
grep -nE 'HandleFunc\("[^"]+"' internal/api/*.go 2>/dev/null \
  | sed -E 's#^internal/api/([^:]+):([0-9]+):.*HandleFunc\("([^"]+)".*#\3\t\1:\2#' \
  | sort -u -k1,1 \
  | awk -F'\t' '{ printf "<tr><td><code>%s</code></td><td class=\"muted\">internal/api/%s</td></tr>\n", $1, $2 }' \
  > "$ROUTES_FRAG"
# fallback when grep found nothing (never leave the block empty)
[ -s "$ROUTES_FRAG" ] || echo '<tr><td colspan="2" class="muted">No routes found.</td></tr>' > "$ROUTES_FRAG"
replace_block routes "$ROUTES_FRAG"

# ---------- GEN:routecount (badge inline) ----------
tmp="$(mktemp)"
sed -E "s#(<!--GEN:routecount-->)[0-9]+(<!--/GEN:routecount-->)#\1${ROUTES}\2#" "$DOC" > "$tmp" && mv "$tmp" "$DOC"

# ---------- cleanup ----------
rm -f "$METRICS_FRAG" "$LOCBARS_FRAG" "$ROUTES_FRAG"

echo "✓ doc updated: LOC=${LOC} packages=${PKGS} routes=${ROUTES} handlers=${HANDLERS} subcmds=${SUBCMDS}"
echo "  $DOC"
