#!/usr/bin/env bash
# android-publicar-devsigned.sh <versionName> <versionCode> [apk] — publica uma
# release assinada com a CHAVE DE DESENVOLVIMENTO nos DOIS lugares que precisam
# saber dela.
#
# ## Por que existe, e o que ele conserta
#
# `scripts/android-publish.sh` é o caminho do operador: exige o APK assinado
# offline e o índice F-Droid já regenerado com a repokey, nenhum dos quais
# existe nesta VPS por desenho (docs/android-signing-keystore.md). Enquanto a
# chave de release não sai da máquina do operador, as versões de trabalho saem
# daqui assinadas com a chave descartável de `docs/android-chave-dev.md` — e
# esse caminho vinha sendo feito por um rascunho fora do repositório que
# publicava SÓ no repositório F-Droid.
#
# Publicar só ali é publicar pela metade, e o sintoma é exato: o aplicativo
# não oferece a atualização, porque o que ele consulta é o CANAL INCREMENTAL
# (`data/android-updates/manifest.json`, servido por `/api/mobile/v1/app/update`),
# que é outro artefato, produzido por `scripts/android-patches.sh`. Faltando
# esse passo, a única saída é baixar o APK à mão — que é exatamente o que este
# script existe para nunca mais ser necessário.
#
# ## Por que o índice mantém uma janela, e não só a versão nova
#
# `android-patches.sh` monta a janela de patches a partir das versões listadas
# em `index-v2.json`. Um índice com uma versão só produz um canal com apenas o
# artefato completo (~10 MB); com a janela, a atualização vira um patch
# incremental (~1,5 MB medido). Numa internet ruim essa é a diferença entre
# atualizar e desistir — é a razão inteira do canal incremental existir.
#
# O ARQUIVO .apk das versões antigas continua saindo do diretório público: o
# `android-patches.sh` já lê os bytes antigos de `data/android-updates/apks/`
# (o arquivo dele, por hash), então a janela não depende de manter binários
# velhos servidos. O índice guarda a MEMÓRIA das versões; o disco público
# guarda só a atual.
#
# Variáveis de ambiente (override para teste): FDROID_REPO_DIR, UPDATES_DIR,
# PACKAGE_ID, JANELA, APKSIGNER.
set -euo pipefail

fail_cedo() { echo "ERRO: $*" >&2; exit 1; }

VERSION_NAME="${1:?uso: scripts/android-publicar-devsigned.sh <versionName> <versionCode> [apk]}"
VERSION_CODE="${2:?uso: scripts/android-publicar-devsigned.sh <versionName> <versionCode> [apk]}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APK="${3:-$ROOT_DIR/android/app/build/outputs/apk/release/app-release.apk}"

# O ALVO É O data/ QUE O SERVIDOR SERVE, nunca o do diretório em que se está.
#
# Isto não é detalhe de caminho: cada ticket trabalha num worktree próprio
# (`.claude/worktrees/<ticket>`), e cada worktree tem um `data/` de verdade,
# vazio. Um caminho relativo à raiz do repositório publica, em silêncio e com
# saída de sucesso, num diretório que nada serve — o aplicativo continua sem
# ver a versão nova e não há erro nenhum para investigar. Aconteceu na
# primeira execução deste script.
#
# O binário no ar sempre roda com DataDir em VPSM_HOME; o código-fonte pode
# estar em qualquer worktree. São coisas diferentes e o script trata como tais.
VPSM_HOME="${VPSM_HOME:-/opt/panel}"
[ -d "$VPSM_HOME/data" ] || fail_cedo "VPSM_HOME=$VPSM_HOME não tem data/ — aponte VPSM_HOME para a instalação servida"
FDROID_REPO_DIR="${FDROID_REPO_DIR:-$VPSM_HOME/data/fdroid/repo}"
UPDATES_DIR="${UPDATES_DIR:-$VPSM_HOME/data/android-updates}"
JANELA="${JANELA:-5}"
APKSIGNER="${APKSIGNER:-apksigner}"

fail() { echo "ERRO: $*" >&2; exit 1; }

[ -f "$APK" ] || fail "APK não encontrado: $APK"

default_package_id() {
  sed -n 's/^vpsmanager\.applicationId=//p' "$ROOT_DIR/android/gradle.properties" | head -1 | tr -d '[:space:]'
}
PACKAGE_ID="${PACKAGE_ID:-$(default_package_id)}"
[ -n "$PACKAGE_ID" ] || fail "PACKAGE_ID vazio"

# Um APK NÃO ASSINADO é o erro silencioso caro deste caminho: o Gradle produz
# `app-release-unsigned.apk` quando a property da chave de dev não é passada, o
# nome é parecido, e o aparelho só recusa no fim de um download inteiro. Vale um
# gate aqui.
if command -v "$APKSIGNER" >/dev/null 2>&1; then
  "$APKSIGNER" verify "$APK" >/dev/null 2>&1 || fail "o APK não está assinado (ou a assinatura é inválida): $APK"
else
  echo "aviso: apksigner ausente — assinatura não verificada" >&2
fi

mkdir -p "$FDROID_REPO_DIR"

# O NOME DE ARQUIVO e o versionName são coisas diferentes, de propósito.
#
# O build de dev acrescenta "-devsigned" ao versionName (app/build.gradle.kts),
# para que um artefato assinado com a chave descartável se identifique como tal
# em `aapt2 dump badging`, na tela de sobre e em qualquer índice. Mas o NOME do
# arquivo público é `vpsm-<versão>.apk` — é ele que vira link, e um link já
# divulgado não pode mudar de forma.
#
# O versionName vem do PRÓPRIO APK, nunca do argumento: o app compara o que o
# índice diz com o que ele tem instalado, e um índice que anuncia "0.1.24" para
# um APK que se chama "0.1.24-devsigned" faz a faixa de atualização mentir
# sobre o que vai ser instalado.
NOME_ARQUIVO="vpsm-$VERSION_NAME.apk"
VERSION_NAME_REAL="$VERSION_NAME"
AAPT2="${AAPT2:-$(command -v aapt2 || ls /opt/android-sdk/build-tools/*/aapt2 2>/dev/null | sort -r | head -1)}"
if [ -n "${AAPT2:-}" ] && [ -x "$AAPT2" ]; then
  lido="$("$AAPT2" dump badging "$APK" 2>/dev/null | sed -n "s/.*versionName='\([^']*\)'.*/\1/p" | head -1)"
  [ -n "$lido" ] && VERSION_NAME_REAL="$lido"
  lido_code="$("$AAPT2" dump badging "$APK" 2>/dev/null | sed -n "s/.*versionCode='\([^']*\)'.*/\1/p" | head -1)"
  if [ -n "$lido_code" ] && [ "$lido_code" != "$VERSION_CODE" ]; then
    fail "o APK tem versionCode=$lido_code, mas você pediu $VERSION_CODE — publicar assim faria o canal mentir"
  fi
else
  echo "aviso: aapt2 ausente — versionName não conferido contra o APK" >&2
fi

python3 - "$FDROID_REPO_DIR" "$UPDATES_DIR" "$PACKAGE_ID" "$APK" \
         "$NOME_ARQUIVO" "$VERSION_NAME_REAL" "$VERSION_CODE" "$JANELA" <<'PY'
# -*- coding: utf-8 -*-
"""Atualiza o registro de versões e reescreve o índice a partir dele."""
import hashlib, json, io, os, sys

repo, updates, pkg, apk, nome, ver, code, janela = sys.argv[1:9]
code, janela = int(code), int(janela)

def sha256(caminho):
    h = hashlib.sha256()
    with open(caminho, "rb") as fh:
        for bloco in iter(lambda: fh.read(1 << 20), b""):
            h.update(bloco)
    return h.hexdigest()

# ── o registro: a memória das versões, independente do que está em disco ────
# Sem ele, cada publicação apagaria a janela de patches junto com os binários
# antigos, e toda atualização voltaria a ser um download completo.
registro_path = os.path.join(repo, "versoes.json")
registro = []
if os.path.exists(registro_path):
    with io.open(registro_path, encoding="utf-8") as fh:
        registro = json.load(fh)

# Semeadura na primeira execução: o canal incremental já arquivou os APKs das
# versões anteriores (apks/<sha>.apk + .versioncode) e o manifesto guarda o
# nome de cada base. Reconstruir o registro daí é o que evita que a primeira
# atualização por este caminho seja um completo de 10 MB sem necessidade.
if not registro:
    manifesto = os.path.join(updates, "manifest.json")
    if os.path.exists(manifesto):
        with io.open(manifesto, encoding="utf-8") as fh:
            m = json.load(fh)
        vistos = {}
        alvo = m.get("latest", {})
        if alvo.get("sha256"):
            vistos[int(alvo["version_code"])] = (alvo["version_name"], alvo["sha256"], int(alvo.get("size_bytes") or 0))
        for p in m.get("patches", []):
            if p.get("from_sha256") and p.get("from_version_code"):
                c = int(p["from_version_code"])
                arquivado = os.path.join(updates, "apks", p["from_sha256"] + ".apk")
                tamanho = os.path.getsize(arquivado) if os.path.exists(arquivado) else 0
                vistos.setdefault(c, (p.get("from_version_name", ""), p["from_sha256"], tamanho))
        for c, (n, s, t) in vistos.items():
            registro.append({"version_code": c, "version_name": n, "sha256": s,
                             "size_bytes": t, "file": "vpsm-%s.apk" % n})
        if registro:
            sys.stderr.write("registro semeado com %d versão(ões) do canal incremental\n" % len(registro))

novo = {
    "version_code": code,
    "version_name": ver,
    "sha256": sha256(apk),
    "size_bytes": os.path.getsize(apk),
    "file": nome,
}
registro = [v for v in registro if int(v["version_code"]) != code] + [novo]
registro.sort(key=lambda v: int(v["version_code"]), reverse=True)
registro = registro[:janela]

# ── disco público: só a versão nova ────────────────────────────────────────
# Os bytes das antigas continuam em updates/apks/ (por hash), que é de onde o
# android-patches.sh lê. Servir binários velhos não traria nada.
import shutil
destino = os.path.join(repo, nome)
if os.path.abspath(apk) != os.path.abspath(destino):
    shutil.copy2(apk, destino)
os.chmod(destino, 0o644)
for f in os.listdir(repo):
    if f.startswith("vpsm-") and f.endswith(".apk") and f != nome:
        os.remove(os.path.join(repo, f))

with io.open(registro_path, "w", encoding="utf-8") as fh:
    json.dump(registro, fh, indent=2, ensure_ascii=False)

versoes = {}
for v in registro:
    versoes[v["sha256"]] = {
        "manifest": {"versionCode": int(v["version_code"]), "versionName": v["version_name"]},
        "file": {"name": "/" + v["file"], "sha256": v["sha256"], "size": int(v["size_bytes"])},
    }
idx = {"repo": {"name": "vps-manager"}, "packages": {pkg: {"versions": versoes}}}
with io.open(os.path.join(repo, "index-v2.json"), "w", encoding="utf-8") as fh:
    json.dump(idx, fh, indent=2)

print("publicado %s · sha %s · janela de %d versão(ões)" % (nome, novo["sha256"][:16], len(registro)))
PY

# ── a outra metade: o canal que o APLICATIVO consulta ──────────────────────
# Sem esta linha o app nunca fica sabendo da versão nova. Era o passo que
# faltava, e o motivo de a atualização só sair por download manual.
echo "── gerando o canal incremental (android-patches.sh) ──"
ANDROID_UPDATES_DIR="$UPDATES_DIR" FDROID_REPO_DIR="$FDROID_REPO_DIR" \
  PACKAGE_ID="$PACKAGE_ID" PATCH_WINDOW="$JANELA" \
  "$ROOT_DIR/scripts/android-patches.sh"

echo "pronto: $VERSION_NAME_REAL ($VERSION_CODE) está no repositório E no canal do app"
