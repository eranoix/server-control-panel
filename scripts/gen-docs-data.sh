#!/usr/bin/env bash
#
# gen-docs-data.sh — regenera os blocos derivados do repo dentro do relatorio
# tecnico HTML, mantendo-o a prova de drift. Substitui SO o conteudo entre os
# marcadores <!-- GEN:nome --> ... <!-- /GEN:nome -->; o resto (texto curado,
# diagramas, convencoes) e preservado.
#
# Blocos gerados:
#   GEN:metrics     — cards de metrica (LOC, pacotes, rotas, handlers, ...)
#   GEN:locbars     — barras de LOC por subsistema (top 10)
#   GEN:routes      — inventario completo de rotas (path + arquivo:linha)
#   GEN:routecount  — numero no badge da topnav (inline)
#
# Uso:  scripts/gen-docs-data.sh
# Idempotente: rodar de novo so atualiza os numeros se o repo mudou.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
DOC=".docs/Documentacao Tecnica - VPS Manager.html"

[ -f "$DOC" ] || { echo "✗ $DOC nao existe" >&2; exit 1; }

# ---------- coleta de metricas (verificadas no repo) ----------
LOC=$(find internal cmd -name '*.go' 2>/dev/null | xargs wc -l 2>/dev/null | tail -1 | awk '{print $1}')
PKGS=$(ls -d internal/*/ 2>/dev/null | wc -l | tr -d ' ')
HANDLERS=$(ls internal/api/handlers_*.go 2>/dev/null | wc -l | tr -d ' ')
ROUTES=$(grep -rhoE 'HandleFunc\("[^"]+"' internal/api/*.go 2>/dev/null | sort -u | wc -l | tr -d ' ')
SUBCMDS=$(grep -rhoE 'case "[a-z][a-z-]+"' cmd/vpsmctl/*.go 2>/dev/null | sort -u | wc -l | tr -d ' ')

# ---------- helper: substitui conteudo entre marcadores ----------
# replace_block NOME ARQUIVO_FRAGMENTO
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
  <div class="card metric"><div class="value">${PKGS}</div><div class="label">Pacotes em internal/</div><div class="metric-bar"><div class="metric-bar-fill" style="width:85%"></div></div></div>
  <div class="card metric"><div class="value">${ROUTES}</div><div class="label">Rotas HTTP/WS</div><div class="metric-bar"><div class="metric-bar-fill" style="width:100%"></div></div></div>
  <div class="card metric"><div class="value">${HANDLERS}</div><div class="label">Arquivos de handler</div><div class="metric-bar"><div class="metric-bar-fill" style="width:65%"></div></div></div>
  <div class="card metric"><div class="value">2</div><div class="label">Binarios (server + ctl)</div><div class="metric-bar"><div class="metric-bar-fill" style="width:20%"></div></div></div>
  <div class="card metric"><div class="value">${SUBCMDS}</div><div class="label">Subcomandos vpsmctl</div><div class="metric-bar"><div class="metric-bar-fill" style="width:80%"></div></div></div>
</div>
EOF
replace_block metrics "$METRICS_FRAG"

# ---------- GEN:locbars (top 10 por LOC) ----------
LOCBARS_FRAG="$(mktemp)"
{
  echo '<div style="display:flex;flex-direction:column;gap:6px;font-size:12px">'
  # maior valor (pra escala)
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

# ---------- GEN:routes (inventario: path + arquivo:linha) ----------
ROUTES_FRAG="$(mktemp)"
grep -nE 'HandleFunc\("[^"]+"' internal/api/*.go 2>/dev/null \
  | sed -E 's#^internal/api/([^:]+):([0-9]+):.*HandleFunc\("([^"]+)".*#\3\t\1:\2#' \
  | sort -u -k1,1 \
  | awk -F'\t' '{ printf "<tr><td><code>%s</code></td><td class=\"muted\">internal/api/%s</td></tr>\n", $1, $2 }' \
  > "$ROUTES_FRAG"
# fallback se grep nao achou nada (nao deixa o bloco vazio)
[ -s "$ROUTES_FRAG" ] || echo '<tr><td colspan="2" class="muted">Nenhuma rota encontrada.</td></tr>' > "$ROUTES_FRAG"
replace_block routes "$ROUTES_FRAG"

# ---------- GEN:routecount (badge inline) ----------
tmp="$(mktemp)"
sed -E "s#(<!--GEN:routecount-->)[0-9]+(<!--/GEN:routecount-->)#\1${ROUTES}\2#" "$DOC" > "$tmp" && mv "$tmp" "$DOC"

# ---------- limpeza ----------
rm -f "$METRICS_FRAG" "$LOCBARS_FRAG" "$ROUTES_FRAG"

echo "✓ doc atualizado: LOC=${LOC} pacotes=${PKGS} rotas=${ROUTES} handlers=${HANDLERS} subcmds=${SUBCMDS}"
echo "  $DOC"
