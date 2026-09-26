#!/usr/bin/env bash
# test-android-patches.sh — cobre scripts/android-patches.sh de ponta a
# ponta, com hdiffz/hpatchz de verdade, sem depender de um build Gradle.
#
# Teste 1: patch aplicado com hpatchz reconstrói bytes IDÊNTICOS ao alvo.
# Teste 2: o artefato "completo" (base vazia) também reconstrói o alvo.
# Teste 3: manifesto coerente — hashes/tamanhos batem com os arquivos.
# Teste 4: idempotência — rodar de novo não regera nem corrompe nada.
# Teste 5: RETENÇÃO — publicar a 6ª versão apaga o que saiu da janela.
# Teste 6: uma versão do índice cujo APK sumiu não derruba a geração.
#
# Os "APKs" aqui são arquivos binários sintéticos derivados uns dos outros
# (é o que hdiffz enxerga: bytes). A prova com APK real assinado, incluindo
# a verificação de que a assinatura sobrevive à reconstrução, está no
# runbook docs/android-atualizacao-incremental.md §5.
set -uo pipefail

RAIZ="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$RAIZ/scripts/android-patches.sh"
PACOTE="tech.northwind.vpsm.app"

[ -f "$SCRIPT" ] || { echo "não achei $SCRIPT"; exit 2; }
command -v hdiffz  >/dev/null 2>&1 || { echo "hdiffz não encontrado — rode scripts/setup-hdiffpatch.sh"; exit 2; }
command -v hpatchz >/dev/null 2>&1 || { echo "hpatchz não encontrado — rode scripts/setup-hdiffpatch.sh"; exit 2; }
command -v python3 >/dev/null 2>&1 || { echo "python3 não encontrado"; exit 2; }

pass=0; fail=0
ok() { echo "  OK: $1"; pass=$((pass+1)); }
no() { echo "  FALHOU: $1"; fail=$((fail+1)); }
echo "=== test-android-patches ==="

TMP="$(mktemp -d "${TMPDIR:-/tmp}/vpsm-android-patches-test.XXXXXX")" || exit 2
trap 'case "$TMP" in "${TMPDIR:-/tmp}"/vpsm-android-patches-test.*) rm -rf "$TMP";; esac' EXIT

REPO="$TMP/repo"
UPDATES="$TMP/updates"
mkdir -p "$REPO" "$UPDATES"

# ── fixtures ─────────────────────────────────────────────────────────────
# Cada "APK" é 2 MiB de um padrão comum (para o patch ter o que reaproveitar)
# mais um miolo próprio da versão (para haver diferença real de bytes).
faz_apk() {
  # Duas linhas de propósito: num único `local a=.. b=..$a..`, o bash expande
  # TODAS as palavras antes de o builtin atribuir, então $a ainda não existe
  # ao montar $b (e com `set -u` isso aborta).
  local code="$1"
  local destino="$REPO/vpsmanager-$code.apk"
  python3 - "$destino" "$code" <<'PY'
import sys
destino, code = sys.argv[1], int(sys.argv[2])
comum = (b"vps-manager-payload-comum-" * 4096)[:2 * 1024 * 1024]
proprio = (("versao-%d-" % code).encode() * 4096)[:128 * 1024]
with open(destino, "wb") as fh:
    fh.write(comum[: 1024 * 1024])
    fh.write(proprio)
    fh.write(comum[1024 * 1024 :])
PY
}

# escreve_index <versionCode>... — monta um index-v2.json com exatamente as
# versões pedidas, no mesmo formato que o fdroidserver produz.
escreve_index() {
  python3 - "$REPO/index-v2.json" "$PACOTE" "$@" <<'PY'
import json, sys
destino, pkg = sys.argv[1], sys.argv[2]
versoes = {}
for code in sys.argv[3:]:
    versoes["v" + code] = {
        "manifest": {"versionName": "0.1." + code, "versionCode": int(code)},
        "file": {"name": "/vpsmanager-%s.apk" % code},
    }
with open(destino, "w", encoding="utf-8") as fh:
    json.dump({"packages": {pkg: {"versions": versoes}}}, fh)
PY
}

roda() { FDROID_REPO_DIR="$REPO" ANDROID_UPDATES_DIR="$UPDATES" "$SCRIPT" "$@"; }

campo() { python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));exec("v=d"+sys.argv[2]);print(v)' "$UPDATES/manifest.json" "$1"; }

# ── publicação inicial: versões 1 e 2 ────────────────────────────────────
faz_apk 1; faz_apk 2
escreve_index 1 2
if ! roda > "$TMP/run1.log" 2>&1; then
  echo "  FALHOU: primeira execução do script"; cat "$TMP/run1.log"; exit 1
fi

ALVO_SHA="$(campo '["latest"]["sha256"]')"
FULL_FILE="$(campo '["full"]["file"]')"
PATCH_FILE="$(campo '["patches"][0]["file"]')"
BASE_SHA="$(campo '["patches"][0]["from_sha256"]')"

# ── Teste 1: patch reconstrói bytes idênticos ────────────────────────────
if hpatchz "$UPDATES/apks/$BASE_SHA.apk" "$UPDATES/$PATCH_FILE" "$TMP/recon-patch.bin" >/dev/null 2>&1 \
   && [ "$(sha256sum "$TMP/recon-patch.bin" | cut -d' ' -f1)" = "$ALVO_SHA" ]; then
  ok "patch aplicado com hpatchz reconstrói SHA-256 idêntico ao alvo"
else
  no "patch NÃO reconstrói o alvo"
fi

# ── Teste 2: completo (base vazia) reconstrói o mesmo alvo ───────────────
if hpatchz "" "$UPDATES/$FULL_FILE" "$TMP/recon-full.bin" >/dev/null 2>&1 \
   && [ "$(sha256sum "$TMP/recon-full.bin" | cut -d' ' -f1)" = "$ALVO_SHA" ]; then
  ok "artefato completo (base vazia) reconstrói SHA-256 idêntico ao alvo"
else
  no "artefato completo NÃO reconstrói o alvo"
fi

# ── Teste 3: manifesto coerente com os arquivos em disco ────────────────
if python3 - "$UPDATES" <<'PY'
import hashlib, json, os, sys
d = sys.argv[1]
m = json.load(open(os.path.join(d, "manifest.json"), encoding="utf-8"))
for art in [m["full"]] + m["patches"]:
    caminho = os.path.join(d, art["file"])
    if not os.path.isfile(caminho):
        sys.exit("artefato %s nao existe" % art["file"])
    if os.path.getsize(caminho) != art["size_bytes"]:
        sys.exit("size_bytes de %s nao bate" % art["file"])
    h = hashlib.sha256(open(caminho, "rb").read()).hexdigest()
    if h != art["sha256"]:
        sys.exit("sha256 de %s nao bate" % art["file"])
if m["schema_version"] != 1 or not m["patch_tool"]:
    sys.exit("cabecalho do manifesto incompleto")
PY
then ok "manifesto coerente: sha256/size_bytes batem com os arquivos"
else no "manifesto incoerente com os arquivos em disco"
fi

# ── Teste 4: idempotência ────────────────────────────────────────────────
antes="$(sha256sum "$UPDATES/$PATCH_FILE" | cut -d' ' -f1)"
if roda > "$TMP/run2.log" 2>&1 \
   && [ "$(sha256sum "$UPDATES/$PATCH_FILE" | cut -d' ' -f1)" = "$antes" ] \
   && grep -q "já existe" "$TMP/run2.log"; then
  ok "rodar de novo é idempotente (reaproveita os artefatos existentes)"
else
  no "segunda execução não foi idempotente"; cat "$TMP/run2.log"
fi

# ── Teste 5: retenção ao publicar a 6ª versão ───────────────────────────
for c in 3 4 5; do faz_apk "$c"; done
escreve_index 1 2 3 4 5
roda > "$TMP/run5.log" 2>&1 || { echo "  FALHOU: execução com 5 versões"; cat "$TMP/run5.log"; }

# Com 5 versões e janela 5: alvo = 5, bases = 4,3,2,1 -> 4 patches, nada some.
n_patches="$(python3 -c 'import json,sys;print(len(json.load(open(sys.argv[1]))["patches"]))' "$UPDATES/manifest.json")"
patch_da_v1="$(python3 -c '
import json,sys
d=json.load(open(sys.argv[1]))
print(next((p["file"] for p in d["patches"] if p["from_version_code"]==1), ""))' "$UPDATES/manifest.json")"
if [ "$n_patches" = "4" ] && [ -n "$patch_da_v1" ] && [ -f "$UPDATES/$patch_da_v1" ]; then
  ok "janela de 5: 4 patches gerados, a base mais antiga ainda dentro"
else
  no "janela de 5 inesperada (patches=$n_patches, patch da v1=$patch_da_v1)"
fi

# Agora a 6ª: a v1 sai da janela e tudo dela tem que sumir.
sha_v1="$(sha256sum "$REPO/vpsmanager-1.apk" | cut -d' ' -f1)"
full_antigo="$FULL_FILE"
faz_apk 6
escreve_index 1 2 3 4 5 6
roda > "$TMP/run6.log" 2>&1 || { echo "  FALHOU: execução com 6 versões"; cat "$TMP/run6.log"; }

erros=""
[ -f "$UPDATES/$patch_da_v1" ] && erros="$erros patch-da-v1-sobreviveu"
[ -f "$UPDATES/apks/$sha_v1.apk" ] && erros="$erros apk-da-v1-sobreviveu"
[ -f "$UPDATES/$full_antigo" ] && erros="$erros full-do-alvo-antigo-sobreviveu"
python3 -c '
import json,sys
d=json.load(open(sys.argv[1]))
assert d["latest"]["version_code"]==6, d["latest"]
assert len(d["patches"])==4, len(d["patches"])
assert sorted(p["from_version_code"] for p in d["patches"])==[2,3,4,5], d["patches"]
' "$UPDATES/manifest.json" 2>/dev/null || erros="$erros manifesto-da-6a-errado"

if [ -z "$erros" ]; then
  ok "6ª versão: patches/APK da base fora da janela apagados, manifesto só com 2..5"
else
  no "retenção falhou:$erros"
fi

# Nenhum .tmp de hdiffz interrompido pode ficar para trás.
if [ -z "$(find "$UPDATES" -name '*.tmp' -o -name '.manifest-*' 2>/dev/null)" ]; then
  ok "nenhum arquivo temporário sobrou no diretório de updates"
else
  no "sobraram temporários: $(find "$UPDATES" -name '*.tmp' -o -name '.manifest-*')"
fi

# ── Teste 6: versão no índice sem o APK correspondente ──────────────────
# O operador podou um APK antigo do pacote dele. A geração não pode morrer:
# aquela base só deixa de ter patch (o app cai no completo).
rm -f "$REPO/vpsmanager-2.apk" "$UPDATES/apks"/*.apk.versioncode
if roda > "$TMP/run7.log" 2>&1; then
  ok "APK ausente no repositório não derruba a geração (degrada só aquela base)"
else
  no "geração morreu com um APK ausente no repositório"; cat "$TMP/run7.log"
fi

echo
echo "=== $pass OK, $fail falha(s) ==="
[ "$fail" -eq 0 ] || exit 1
