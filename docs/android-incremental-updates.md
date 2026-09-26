# Incremental updates for the Android app (HDiffPatch)

How the app updates itself without downloading 31 MB every time. **End to end,
done and proven**: server (routes + generation), `:patch-engine` (applies and
checks) and device (banner, resumable download, install) — see §7.

Numbers measured in this project, `0.1.5 → 0.1.6` (real, signed APKs):

| artifact | size | vs. the raw APK |
|---|---:|---:|
| raw APK (what F-Droid serves today) | 31,135,416 B | — |
| `.7z` (what is delivered by hand today) | 10,019,779 B | −68% |
| **full** (`hdiffz` with an empty base) | 10,029,237 B | **−68%** |
| **patch** `0.1.5 → 0.1.6` | **1,400,329 B** | **−95.5%** |

Against today's `.7z`, the patch is **−86%**.

---

## 1. Decisions and the reason for each

**Tool: HDiffPatch (`hdiffz`/`hpatchz`), MIT.** Google's `archive-patcher` was
rejected with evidence: archived in 2024, broken on Android 11+, and it would
only attack 4 KB — because 29.47 of this APK's 31.14 MB are STORED (the `.so`
files and `classes.dex` are written uncompressed so Android can map them
directly).

**Keyed by the SHA-256 of the base, never by `versionCode`.** The patch is a
function of the exact BYTES of the installed APK. `versionCode` does not derive
from content: two builds of the same `versionCode` (a rebuild, a re-signing, a
different ABI) have different bytes, and applying the wrong patch does not
produce a version error — it produces a corrupt file. The app sends the hash of
what it has installed; with no patch for that exact hash, it falls back to the
full download.

**Direct patches from the latest versions, not chained.** Skipping a version in a
direct patch cost 3.0 MB (still −70%); a chain of two patches would cost two
downloads and two applications to reach the same place, with twice the failure
points.

**The "full" download is also a `.hdiff`.** `hdiffz` with an empty base produces
10.03 MB and reconstructs bytes identical to the signed APK. The device is left
with ONE code path (`hpatchz`) instead of a "download the APK" path and an
"apply a patch" path side by side.

**Compression `-SD -c-lzma2-9-64m`.** Measured:

```
-c-zstd-21-24        1 583 566      -SD -c-zstd-21-24     1 551 505
-c-lzma2-9-64m       1 415 213      -SD -c-lzma2-9-64m    1 400 329  <-- chosen
```

`-SD` (the `HDIFFSF20` format) is not only the smallest, it also applies with a
SINGLE decompression buffer (`stepMemSize` measured: 262,140 B for the patch, 7 B
for the full) and supports step-by-step application during the download. Verified
that the prebuilt `libhpatchz.so` from the official Android SDK (v5.1.3,
arm64-v8a) ships `lzma2` and the `-SD` mode. The exact command line is written
into the manifest (`patch_tool`), so that a future incompatibility shows up in
diagnostics instead of becoming "it does not update and nobody knows why".

**Authenticated route.** This is not encryption: the APK signature is still the
final gate that decides what Android installs. The reason is different — a binary
patch is a map of which parts of the code changed, and the repository is private.
Encrypting at rest would solve nothing (whoever has a session has the key); taking
it out of anonymous access does.

---

## 2. Installing the tool

```bash
scripts/setup-hdiffpatch.sh      # downloads the official v5.1.3 binary, checks SHA-256
```

Installs `hdiffz` and `hpatchz` into `/usr/local/bin`. Idempotent.

---

## 3. Generation (automatic, at the end of publishing)

`scripts/android-publish.sh` calls `scripts/android-patches.sh` as its last step
— **after** the gate that checks the fingerprint with `apksigner` and after the
`rsync` into `data/fdroid/repo/`.

The order is not cosmetic: the patch has to come from the bytes of the
**already-signed** APK. Signing is offline by design
(`docs/android-signing-keystore.md` §1), so this is the only moment the VPS has
the final bytes in hand. Generating from the unsigned artifact would produce a
patch that reconstructs a file Android refuses to install.

A failure during generation **does not undo the publish**: the F-Droid repository
is already live and is the channel that always works. Without a catalog, the app
merely loses the incremental path (`GET /app/update` answers 503, "channel not
published yet").

To run it by hand: `scripts/android-patches.sh`
(overrides: `FDROID_REPO_DIR`, `ANDROID_UPDATES_DIR`, `PACKAGE_ID`,
`PATCH_WINDOW`, `HDIFFZ`).

### What stays on disk

```
data/android-updates/
  apks/<sha256>.apk                  signed APK archived, by hash
  apks/<sha256>.apk.versioncode      versionCode, to find the copy again
  patches/<shaBase>-<shaTarget>.hdiff  direct patch
  full/<shaTarget>.hdiff             full reconstruction
  manifest.json                      index read by internal/androidupdate
```

**Why we archive the APKs.** Generating the NEXT version's patch needs the bytes
of the previous ones. `data/fdroid/repo/` is replaced wholesale
(`rsync --delete-after`) by the package the operator sends, so the history there
depends on the operator not having pruned anything — and in `fdroidserver`, the
`archive_older` option moves old versions to another repository in exactly that
way. Archiving here (hardlinked when the filesystem is the same: zero disk cost
while both names exist) makes generation independent of that. If an APK
disappears from the repository AND from the archive, that base simply stops
having a patch — the app falls back to the full download, nothing breaks.

**Retention: `PATCH_WINDOW=5`.** The 5 newest versions, including the target —
that is, the target + 4 bases. When the 6th is published, everything whose base
fell out of the window (patch and archived APK) is deleted, along with the
previous target's `full/`.

---

## 4. API (authenticated, inside the mobile BFF)

```
GET /api/mobile/v1/app/update?base_sha256=<hex>
```

```json
{
  "latest":   { "version_name": "0.1.6", "version_code": 6,
                "sha256": "<hash of the signed APK>", "size_bytes": 31135416 },
  "up_to_date": false,
  "patch":    { "url": "...", "size_bytes": 1400329, "sha256": "<hash of the .hdiff>" },
  "full":     { "url": "...", "size_bytes": 10029237, "sha256": "<hash of the .hdiff>" },
  "patch_tool": "HDiffPatch::hdiffz v5.1.3 -SD -c-lzma2-9-64m"
}
```

- `patch` is **`null`** whenever no patch exists for that exact base (unknown
  base, version outside the window, or `base_sha256` omitted). It never
  approximates.
- `full` is **always** present — including alongside the patch, so that a failure
  to apply does not require a second round trip to the server.
- The `size_bytes`/`sha256` of the artifacts describe the **`.hdiff`** (what goes
  over the wire), not the APK it reconstructs. `latest.size_bytes` is the APK.
- With no catalog published: **503**, not 404 — it is a server state, not the
  wrong resource, and the app needs to tell the two apart.

```
GET /api/mobile/v1/app/update/artifact?file=<file field from the manifest>
```

Serves the bytes with `http.ServeContent`: **Range, `If-Range`, `If-None-Match`,
206 with `Content-Range`, 416 on an impossible range** — that is, genuinely
resumable downloads. The `ETag` is the SHA-256 of the artifact and `Cache-Control`
is `immutable`, which is correct because the file name already is its content.

`file` is resolved by **exact string comparison against the manifest**, which
works as an allowlist: what is not in the manifest does not exist, even if it
exists on disk. It is the only door through which a name coming off the network
becomes a path on disk.

---

## 5. How it is proven

`scripts/test-android-patches.sh` (8 cases, real `hdiffz`/`hpatchz`): patch and
full both reconstruct an identical SHA-256; the manifest is consistent with the
files; idempotency; **retention when the 6th version is published**; no leftover
temporary files; a missing APK in the repository does not break generation.

Go tests: `internal/androidupdate` (manifest validation, rejection of absolute
paths/traversal/unknown schema), `internal/mobilebff` (patch selection, fallback,
401, 206/416/404).

Proof with a real signed APK (`0.1.5` → `0.1.6`, dev key):

```
patch  1 400 329 B  ->  hpatchz  ->  sha256 4456a4ca…a999  == new APK  ✅
full  10 029 237 B  ->  hpatchz  ->  sha256 4456a4ca…a999  == new APK  ✅
apksigner verify on the reconstructed APK: signature intact  ✅
```

And over HTTP, with a real `Bearer` token: `curl -r 100-200` → `206`,
`Content-Range: bytes 100-200/1400329`, bytes identical to the file on disk;
downloading in two chunks and concatenating reproduces the `.hdiff` byte for
byte, and the reassembled patch still reconstructs the APK.

---

## 6. F-Droid channel: why NOT to compress the APK in transit

`/fdroid/repo/*.apk` serves 31 MB where the `.7z` is 10 MB, and 29.47 MB of the
APK are STORED — so there is a lot to gain in transit. It was evaluated and
**rejected**, with evidence from the client's code.

`gzip -9` would take the APK to 13,441,631 B (−57%, about 17.7 MB saved). But the
F-Droid client (`libs/download/.../HttpManager.kt`) does both of these at once:

```kotlin
install(ContentEncoding) { deflate(); gzip() }          // decodes on its own
if (skipFirstBytes > 0) header(Range, "bytes=$skipFirstBytes-")
if (skipBytes > 0L && response.status != PartialContent) throw NoResumeException()
```

and it counts `skipBytes` in **decoded** bytes. On resume it would ask for offset
N of the APK, and the server would answer with offset N of the **gzip** stream —
different bytes, spliced together silently, detected only by the final hash
check. The result: the download would restart forever on a bad connection.
Trading 17.7 MB of savings in the clean case for broken resume in the bad case is
the worst deal available to this server.

The real gain comes from the other side, and it is bigger: the incremental
channel described in this document delivers **10.03 MB** for a full install and
**1.40 MB** for an update. F-Droid remains the first-install and rescue path.

---

## 7. Device side: banner, resumable download and install

What runs on the phone, and why each piece sits where it does.

```
:data/update/          network, disk and orchestration (the only place with network)
  InstalledApkReader   SHA-256 of the installed APK -> becomes base_sha256
  UpdateRepository     GET /app/update  +  Range download (resumable)
  UpdateStaging        filesDir/atualizacoes/ + space reservation
  UpdateCoordinator    the fallback ladder and the StateFlow the banner draws
  ApkInstaller         PackageInstaller (session, pre-approval, commit)
  UpdateInstallReceiver  system result -> UpdateDiagnostics
:patch-engine          hpatchz + SHA-256 check (no network, no UI)
:app/update/           UpdateBanner (top of the shell) + UpdateCheckWorker (6 h)
```

### Decisions that are not obvious

**The banner shows the size of what TRAVELS.** "Versão 0.1.7 disponível —
1,4 MB", never the 31 MB of the reconstructed APK. On a bad connection that
number is the most important information on the screen. Decimal MB (10⁶) — the
same unit as Android's own Settings and the same as the numbers in this document.

**The strip lives in the `Scaffold`'s `topBar` slot**, in a `Column` with the
`TopAppBar`. It is the only spot that survives every destination of the drawer,
and `Scaffold` subtracts its height from `innerPadding` by itself. It disappears
on detail screens (terminal, call, editor) under the same condition that already
hides the bar: there, every dp belongs to the content.

**`If-Range` is mandatory on resume.** `Range: bytes=N-` on its own would
silently splice bytes of a target that changed onto what is already on disk. With
a strong ETag (which IS the SHA-256 of the artifact), a different target returns
200 instead of 206 and the client truncates and starts over. All three responses
(206, 200, 416) are handled explicitly, and the offset comes from the server's
`Content-Range` — never from what was requested.

**Staging in `filesDir`, never `cacheDir`.** `PackageInstaller`'s own
preallocation evicts app caches to free space: downloading 10 MB into the cache
and then asking the installer for space can delete the file you just downloaded.

**`applicationInfo.sourceDir` is read every time**, never stored: the path has two
random segments that Android changes on every install. The hash is cached keyed by
that path, which makes the cache invalidate itself — there is no way to ask for
the previous version's patch.

**The installer's `PendingIntent` is MUTABLE.** It is the system that fills in
`EXTRA_STATUS`/`EXTRA_STATUS_MESSAGE`; with `targetSdk` 35+, `commit()` throws
`IllegalArgumentException` if it is immutable — and lint recommends the immutable
one.

**No silent updates.** `UPDATE_PACKAGES_WITHOUT_USER_ACTION` exists and an app
that updates itself qualifies, but `SilentUpdatePolicy`'s 30 s limiter makes it
fall back to the dialog intermittently. A flow that sometimes asks would look like
a bug.

**`requestUserPreapproval` asks BEFORE the download** (API 34; `minSdk` is 34).
Two traps found on the emulator, both fatal and silent:

1. The `label` has to be EXACTLY the app's label. "VPS Manager 0.1.7" made the
   system answer `INSTALL_FAILED_INTERNAL_ERROR: PreapprovalDetails { ... }
   inconsistent with app label`.
2. When pre-approval fails, the system **destroys the session**. Committing on a
   dead session reports no outcome at all and the banner would hang on
   "Instalando…" forever. The session is recreated before the commit, and the
   coordinator has a safety net that turns any unexpected exception into an error
   state with a path to Diagnostics.

### The fallback ladder

| situation | behavior |
|---|---|
| unknown base (rebuild, dev build) | full (29 MB in the lab, 10 MB in release) |
| `.hdiff` with the wrong SHA-256 | retries once, then steps down |
| connection drop | does NOT step down (the full would drop too); resumes by Range |
| **reconstructed APK with a different hash** | discards it, falls back to the full, records the base hash in Diagnostics |
| `hpatchz` error | full |
| not enough space | says how many MB are missing and does not start |
| install fails | keeps the APK (retrying does not step down) and shows `EXTRA_STATUS_MESSAGE` in Diagnostics |
| unknown sources denied | deep-links straight to the app's toggle |
| device blocks sideloading | says so clearly and points to `/android/install` |

The inviolable rule: **an APK whose SHA-256 was not checked never reaches the
installer**. `hpatchz` over the wrong base returns SUCCESS and writes a complete,
wrong file — its exit code is evidence of nothing.

### How it is proven

68 new JVM tests (843 in total, zero failures), including: a real resume against
`MockWebServer` (the server delivers half and stops; checked that the partial
stays on disk, that the second trip sends `Range: bytes=<half>-` with the ETag's
`If-Range`, and that the final file is byte-for-byte identical); "an APK with a
divergent hash never reaches the installer"; and one test per rung of the ladder.

End to end on the emulator (Android 16, API 36), twice:

```
0.1.6 installed -> banner "Versão 0.1.7 disponível — 4 KB"
   -> pre-approval BEFORE the download -> patch 4 803 B -> hpatchz
   -> SHA-256 matches -> installs -> app opens at 0.1.7, session intact  ✅

unknown base -> banner "Versão 0.1.7 disponível — 29,0 MB"
   -> "Baixando 0.1.7 — 11,6 MB de 29,0 MB" with a progress bar and "Cancelar"
   -> full (empty base) -> installs -> 0.1.7                             ✅
```

The lab numbers come from DEBUG APKs (111 MB, no R8): they prove the flow, not
the size of the channel. The real channel sizes are the ones in the sections
above — 1.40 MB for the patch, 10.03 MB for the full.
