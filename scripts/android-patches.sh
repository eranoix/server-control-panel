#!/usr/bin/env bash
# android-patches.sh — gera o catálogo de atualização incremental do app
# Android a partir do que ACABOU de ser publicado em data/fdroid/repo/.
#
# É a etapa final de scripts/android-publish.sh, executada DEPOIS do gate que
# confere o fingerprint com apksigner. A ordem não é estética: o patch é
# gerado a partir dos bytes do APK **já assinado**, nunca do artefato
# não-assinado do Gradle. A assinatura é offline por desenho
# (docs/android-signing-keystore.md §1), então o único momento em que este
# host tem em mãos os bytes finais é depois da publicação — e é exatamente aí
# que este script roda.
#
# O QUE PRODUZ (data/android-updates/)
#   apks/<sha256>.apk                  arquivo do APK assinado, por hash
#   patches/<shaBase>-<shaAlvo>.hdiff  patch direto base -> versão nova
#   full/<shaAlvo>.hdiff               reconstrução completa (base vazia)
#   manifest.json                      índice lido por internal/androidupdate
#
# POR QUE CHAVEADO POR SHA-256, NÃO POR versionCode
# O patch depende dos BYTES exatos da base. versionCode não deriva do
# conteúdo — dois builds do mesmo versionCode têm bytes diferentes, e aplicar
# o patch errado não dá erro de versão, dá arquivo corrompido. O app manda o
# hash do APK que tem instalado; sem patch para aquele hash exato, cai para o
# completo.
#
# POR QUE PATCHES DIRETOS, NÃO EM CADEIA
# Cada versão pulada é um ponto de falha a mais e uma aplicação de patch a
# mais no aparelho. Pular uma versão num patch direto custou 3,0 MB (ainda
# −70% contra o APK); uma cadeia de dois patches custaria os dois downloads e
# duas aplicações para chegar no mesmo lugar.
#
# POR QUE O "COMPLETO" TAMBÉM É .hdiff
# hdiffz com base vazia produz ~10 MB (contra 31 MB do APK cru) e reconstrói
# bytes idênticos ao APK assinado. O aparelho fica com UM caminho de código
# (hpatchz) para os dois casos, em vez de um "baixar APK" e um "aplicar
# patch" para manter em paralelo.
#
# POR QUE ARQUIVAR OS APKs EM apks/
# A geração do patch da PRÓXIMA versão precisa dos bytes das anteriores.
# data/fdroid/repo/ é substituído por inteiro (rsync --delete-after) pelo
# pacote que o operador manda, então o histórico ali depende de o operador
# não ter podado nada. Arquivar aqui (hardlink quando possível: custo zero de
# disco enquanto os dois nomes existem) torna a geração independente disso.
#
# Variáveis de ambiente (override para teste; produção usa os defaults):
#   FDROID_REPO_DIR, ANDROID_UPDATES_DIR, PACKAGE_ID, PATCH_WINDOW, HDIFFZ
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FDROID_REPO_DIR="${FDROID_REPO_DIR:-$ROOT_DIR/data/fdroid/repo}"
UPDATES_DIR="${ANDROID_UPDATES_DIR:-$ROOT_DIR/data/android-updates}"
HDIFFZ="${HDIFFZ:-hdiffz}"

# PACKAGE_ID default lido de android/gradle.properties — a MESMA e única
# fonte da verdade que internal/api/handlers_android_install.go usa (e que
# TestAndroidPackageID prende). Nunca repetir o literal aqui: a constante Go
# nasceu errada exatamente assim, e a página de instalação passou a dizer
# "nenhuma versão publicada" para sempre, sem nenhum erro visível.
default_package_id() {
  sed -n 's/^vpsmanager\.applicationId=//p' "$ROOT_DIR/android/gradle.properties" | head -1 | tr -d '[:space:]'
}
PACKAGE_ID="${PACKAGE_ID:-$(default_package_id)}"

# PATCH_WINDOW é o número de versões MAIS NOVAS mantidas no catálogo,
# incluindo o alvo. Com 5: o alvo + 4 bases, e tudo cuja base saiu da janela
# é apagado. Segurar mais que isso é pagar disco por uma base que quase
# ninguém tem instalada.
PATCH_WINDOW="${PATCH_WINDOW:-5}"

# Opções de compressão. Medido neste projeto sobre 0.1.5 -> 0.1.6:
#   -c-zstd-21-24     1 583 566 B      -c-lzma2-9-64m     1 415 213 B
#   -SD -c-zstd-21-24 1 551 505 B      -SD -c-lzma2-9-64m 1 400 329 B  <-- escolhido
# e no completo (base vazia): -SD -c-lzma2-9-64m = 10 029 237 B contra
# 31 135 416 B do APK cru (−68%).
#
# -SD (single compressed diff, HDIFFSF20) além de ser o menor, é o formato
# que o hpatchz aplica com UM só buffer de descompressão e suporta aplicação
# passo-a-passo durante o download — as duas coisas que importam num
# aparelho. Verificado que o libhpatchz.so pré-compilado do SDK Android
# oficial (v5.1.3, arm64-v8a) traz lzma2 e o modo -SD; se isso mudar, a
# string abaixo e o campo patch_tool do manifesto são o ponto único de
# ajuste.
HDIFF_OPTS=(-SD -c-lzma2-9-64m)

fail() { echo "ERRO: $*" >&2; exit 1; }

command -v "$HDIFFZ" >/dev/null 2>&1 || fail "hdiffz não encontrado (\$HDIFFZ=$HDIFFZ) — rode scripts/setup-hdiffpatch.sh"
command -v python3 >/dev/null 2>&1 || fail "python3 não encontrado"
command -v sha256sum >/dev/null 2>&1 || fail "sha256sum não encontrado"

INDEX_JSON="$FDROID_REPO_DIR/index-v2.json"
[ -f "$INDEX_JSON" ] || fail "$INDEX_JSON não existe — nada publicado ainda; rode scripts/android-publish.sh antes"

[ -n "$PACKAGE_ID" ] || fail "PACKAGE_ID vazio e android/gradle.properties não define vpsmanager.applicationId"

mkdir -p "$UPDATES_DIR/apks" "$UPDATES_DIR/patches" "$UPDATES_DIR/full"

# hdiffz sem argumentos imprime o banner de uso e sai com status != 0 — sob
# `set -e -o pipefail` isso abortaria o script aqui, silenciosamente. O
# `|| true` é o que torna a leitura da versão inofensiva.
HDIFF_VERSION="$({ "$HDIFFZ" 2>&1 || true; } | head -1 | tr -d '\r')"
PATCH_TOOL="$HDIFF_VERSION ${HDIFF_OPTS[*]}"

# ── 1. janela de versões, da mais nova para a mais velha ──────────────────
# Saída: uma linha por versão, "versionCode<TAB>versionName<TAB>arquivoAPK".
# A ordenação é feita em Python (numérica por versionCode) e não em sort(1)
# para não depender de locale.
VERSOES="$(python3 - "$INDEX_JSON" "$PACKAGE_ID" "$PATCH_WINDOW" <<'PY'
import json, sys
idx_path, pkg_id, window = sys.argv[1], sys.argv[2], int(sys.argv[3])
with open(idx_path, encoding="utf-8") as fh:
    idx = json.load(fh)
pkg = idx.get("packages", {}).get(pkg_id)
if not pkg:
    sys.stderr.write(
        "ERRO: index-v2.json nao tem o pacote %r.\n"
        "      Pacotes presentes: %s\n" % (pkg_id, ", ".join(sorted(idx.get("packages", {}))) or "(nenhum)")
    )
    sys.exit(3)
vistos = {}
for v in pkg.get("versions", {}).values():
    man = v.get("manifest", {})
    code = man.get("versionCode")
    arquivo = (v.get("file", {}) or {}).get("name", "").lstrip("/")
    if code is None or not arquivo:
        continue
    # Mesmo versionCode aparecendo duas vezes: fica o primeiro; o indice do
    # fdroidserver nao deveria produzir isso, e adivinhar qual vale seria pior
    # que ser deterministico.
    vistos.setdefault(int(code), (man.get("versionName", ""), arquivo))
for code in sorted(vistos, reverse=True)[:window]:
    nome, arquivo = vistos[code]
    print("%d\t%s\t%s" % (code, nome, arquivo))
PY
)" || fail "não consegui ler as versões de $INDEX_JSON"

[ -n "$VERSOES" ] || fail "nenhuma versão de $PACKAGE_ID em $INDEX_JSON"

# ── 2. arquiva cada APK da janela sob apks/<sha256>.apk ───────────────────
# TSV de trabalho: sha256, versionCode, versionName, tamanho.
TRABALHO="$(mktemp "${TMPDIR:-/tmp}/vpsm-android-patches.XXXXXX")"
trap 'rm -f "$TRABALHO" "$TRABALHO.manifest"' EXIT

while IFS=$'\t' read -r code nome arquivo; do
  [ -n "$code" ] || continue
  origem="$FDROID_REPO_DIR/$arquivo"
  if [ ! -f "$origem" ]; then
    # A versão está no índice mas o APK não veio no pacote do operador.
    # Se já arquivamos numa publicação anterior, seguimos com o arquivado;
    # senão essa base simplesmente não gera patch (o app cai no completo).
    # O arquivo .versioncode ao lado de cada APK arquivado existe só para
    # este caso: reencontrar, pelo versionCode do índice, a cópia que
    # guardamos por hash (o hash não pode ser recalculado sem o arquivo
    # original, que é justamente o que falta aqui).
    encontrado=""
    for cand in "$UPDATES_DIR/apks"/*.apk; do
      [ -f "$cand" ] || continue
      cand_code="$(cat "$cand.versioncode" 2>/dev/null || true)"
      if [ "$cand_code" = "$code" ]; then encontrado="$cand"; break; fi
    done
    if [ -z "$encontrado" ]; then
      echo "AVISO: versionCode $code está no índice mas $origem não existe e não há cópia arquivada — sem patch a partir dessa base" >&2
      continue
    fi
    origem="$encontrado"
  fi
  sha="$(sha256sum "$origem" | cut -d' ' -f1)"
  tamanho="$(stat -c%s "$origem")"
  destino="$UPDATES_DIR/apks/$sha.apk"
  if [ ! -f "$destino" ]; then
    # hardlink primeiro: mesmo filesystem, custo zero de disco. O rsync
    # --delete-after de uma publicação futura remove o nome em fdroid/repo,
    # mas o inode sobrevive pelo nome daqui.
    ln "$origem" "$destino" 2>/dev/null || cp -f "$origem" "$destino"
  fi
  printf '%s\n' "$code" > "$destino.versioncode"
  printf '%s\t%s\t%s\t%s\n' "$sha" "$code" "$nome" "$tamanho" >> "$TRABALHO"
done <<< "$VERSOES"

[ -s "$TRABALHO" ] || fail "nenhum APK da janela pôde ser localizado — nada a gerar"

# ── 3. alvo = primeira linha (maior versionCode) ──────────────────────────
IFS=$'\t' read -r ALVO_SHA ALVO_CODE ALVO_NOME ALVO_TAM < "$TRABALHO"
ALVO_APK="$UPDATES_DIR/apks/$ALVO_SHA.apk"

echo "==> alvo: $ALVO_NOME (versionCode $ALVO_CODE) sha256=$ALVO_SHA"

# ── 4. reconstrução completa (base vazia) ────────────────────────────────
FULL_REL="full/$ALVO_SHA.hdiff"
FULL_ABS="$UPDATES_DIR/$FULL_REL"
if [ -s "$FULL_ABS" ]; then
  echo "    completo já existe: $FULL_REL"
else
  echo "    gerando completo (base vazia)..."
  # Grava em .tmp e move: um Ctrl-C no meio nunca deixa um .hdiff truncado
  # com nome definitivo, que o manifesto seguinte publicaria como bom.
  "$HDIFFZ" "${HDIFF_OPTS[@]}" "" "$ALVO_APK" "$FULL_ABS.tmp" >/dev/null \
    || fail "hdiffz falhou gerando o artefato completo"
  mv -f "$FULL_ABS.tmp" "$FULL_ABS"
fi

# ── 5. um patch direto de cada base da janela ────────────────────────────
: > "$TRABALHO.manifest"
printf 'full\t%s\t\t\t\n' "$FULL_REL" >> "$TRABALHO.manifest"

while IFS=$'\t' read -r sha code nome tamanho; do
  [ "$sha" = "$ALVO_SHA" ] && continue
  base_apk="$UPDATES_DIR/apks/$sha.apk"
  [ -f "$base_apk" ] || continue
  rel="patches/$sha-$ALVO_SHA.hdiff"
  abs="$UPDATES_DIR/$rel"
  if [ -s "$abs" ]; then
    echo "    patch já existe: $nome ($code)"
  else
    echo "    gerando patch de $nome ($code)..."
    "$HDIFFZ" "${HDIFF_OPTS[@]}" "$base_apk" "$ALVO_APK" "$abs.tmp" >/dev/null \
      || fail "hdiffz falhou gerando o patch de $sha"
    mv -f "$abs.tmp" "$abs"
  fi
  printf 'patch\t%s\t%s\t%s\t%s\n' "$rel" "$sha" "$nome" "$code" >> "$TRABALHO.manifest"
done < "$TRABALHO"

# ── 6. manifest.json, escrito atomicamente ───────────────────────────────
python3 - "$UPDATES_DIR" "$PACKAGE_ID" "$PATCH_TOOL" "$ALVO_SHA" "$ALVO_CODE" "$ALVO_NOME" "$ALVO_TAM" "$TRABALHO.manifest" <<'PY'
import hashlib, json, os, sys, tempfile, time

(updates_dir, package_id, patch_tool, alvo_sha, alvo_code,
 alvo_nome, alvo_tam, linhas_path) = sys.argv[1:9]


def descreve(rel):
    caminho = os.path.join(updates_dir, rel)
    h = hashlib.sha256()
    with open(caminho, "rb") as fh:
        for bloco in iter(lambda: fh.read(1 << 20), b""):
            h.update(bloco)
    return h.hexdigest(), os.path.getsize(caminho)


full = None
patches = []
with open(linhas_path, encoding="utf-8") as fh:
    for linha in fh:
        if not linha.strip():
            continue
        kind, rel, from_sha, from_nome, from_code = (linha.rstrip("\n").split("\t") + [""] * 5)[:5]
        sha, tamanho = descreve(rel)
        art = {"kind": kind, "file": rel, "size_bytes": tamanho, "sha256": sha}
        if kind == "full":
            full = art
        else:
            art["from_sha256"] = from_sha
            art["from_version_name"] = from_nome
            art["from_version_code"] = int(from_code)
            patches.append(art)

if full is None:
    sys.exit("ERRO: artefato completo ausente ao montar o manifesto")

# Ordem estavel (base mais nova primeiro) para o manifesto nao mudar de bytes
# sem que o conteudo tenha mudado.
patches.sort(key=lambda a: a["from_version_code"], reverse=True)

manifesto = {
    "schema_version": 1,
    "generated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    "package_id": package_id,
    "patch_tool": patch_tool,
    "latest": {
        "version_name": alvo_nome,
        "version_code": int(alvo_code),
        "sha256": alvo_sha,
        "size_bytes": int(alvo_tam),
    },
    "full": full,
    "patches": patches,
}

destino = os.path.join(updates_dir, "manifest.json")
fd, tmp = tempfile.mkstemp(dir=updates_dir, prefix=".manifest-", suffix=".json")
try:
    with os.fdopen(fd, "w", encoding="utf-8") as fh:
        json.dump(manifesto, fh, indent=2, ensure_ascii=False, sort_keys=True)
        fh.write("\n")
        fh.flush()
        os.fsync(fh.fileno())
    os.chmod(tmp, 0o644)
    # Rename atomico: um leitor concorrente (o servidor) ve o manifesto
    # antigo inteiro ou o novo inteiro, nunca um meio-termo.
    os.replace(tmp, destino)
except BaseException:
    if os.path.exists(tmp):
        os.unlink(tmp)
    raise
print("    manifest.json: %d patch(es) + completo" % len(patches))
PY

# ── 7. retenção: apaga tudo que o manifesto novo não referencia ──────────
# Inclui os patches cuja BASE saiu da janela (é o caso que mais consome
# disco) e o artefato completo de qualquer alvo anterior.
python3 - "$UPDATES_DIR" <<'PY'
import json, os, sys

updates_dir = sys.argv[1]
with open(os.path.join(updates_dir, "manifest.json"), encoding="utf-8") as fh:
    m = json.load(fh)

manter = {m["full"]["file"]}
manter.update(p["file"] for p in m["patches"])
apks_vivos = {m["latest"]["sha256"] + ".apk"}
apks_vivos.update(p["from_sha256"] + ".apk" for p in m["patches"])

removidos = 0
for sub in ("patches", "full"):
    d = os.path.join(updates_dir, sub)
    for nome in os.listdir(d):
        rel = sub + "/" + nome
        if rel in manter:
            continue
        os.unlink(os.path.join(d, nome))
        removidos += 1

d = os.path.join(updates_dir, "apks")
for nome in os.listdir(d):
    base = nome[:-len(".versioncode")] if nome.endswith(".versioncode") else nome
    if base in apks_vivos:
        continue
    os.unlink(os.path.join(d, nome))
    removidos += 1

print("    retencao: %d arquivo(s) fora da janela removido(s)" % removidos)
PY

echo "OK: catálogo de atualização incremental em $UPDATES_DIR"
