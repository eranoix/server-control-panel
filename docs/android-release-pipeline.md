# RUNBOOK — Android release pipeline (CI -> offline signing -> F-Droid)

How an `android-v*` tag becomes a release published in this project's
self-hosted F-Droid repository, and why signing — of both the APK and the
repository index — never happens on this VPS.

## 1. Why this is not a fully automated pipeline (the trade-off)

The distribution requirement asks for "a new version is built and
published... by an automated pipeline". An earlier, already locked decision
requires the APK master signing key to **never** exist on this VPS — it is a
public server, and that key has no safety net (there is no Play App Signing
behind it; see `docs/android-signing-keystore.md`). The correction recorded in
`docs/android-fdroid-repo.md` §1-2 extends exactly the same rule to this
project's second key: the `repokey` that signs the F-Droid repository index
**also** never exists on this VPS.

Those two requirements are not compatible with "everything runs by itself":
there is no command or API that signs something with a key which, by design, is
not reachable from where the command runs. The pipeline below automates
literally everything — build, versioning, fingerprint verification, publishing,
regenerating whatever can be regenerated without the key — and isolates the two
operations that require a private key into exactly two manual, offline points on
the operator's machine: signing the APK and signing the index. Apart from that,
nothing else asks for human intervention.

## 2. The two keys and what each one signs

| Key | Lives on | Signs | Never touches this VPS |
|---|---|---|---|
| APK master key | Operator's machine, offline (`docs/android-signing-keystore.md`) | The APK itself (`apksigner sign`) | Yes — since Phase 2 |
| F-Droid `repokey` | Operator's machine, offline (`docs/android-fdroid-repo.md`) | The repository index (`fdroid update`) | Yes — corrected during this phase |

This VPS keeps only the two **public fingerprints** derived from those keys
(`docs/android-signing-keystore.md` §5 and the `fdroid_repo_fingerprint` key in
`data/secrets.vault`) — never the keys, never the passwords.

## 3. versionCode / versionName strategy

- **`versionCode = github.run_number`** — the `android-release` job in
  `.github/workflows/ci.yml` uses `${{ github.run_number }}` directly. GitHub
  guarantees that number is monotonically increasing across the whole history of
  the workflow in the repository, even through reverts or retags — exactly the
  property Android requires to accept a new APK as an update to an installed one
  (same `applicationId`, same signing certificate, strictly greater
  `versionCode`). It does not depend on any hand-maintained state file in this
  repository, so there is no way to forget to bump it.
- **`versionName`** comes literally from whatever was tagged: an
  `android-v1.2.3` tag produces `VERSION_NAME=1.2.3`
  (`${GITHUB_REF#refs/tags/android-v}`). Purely cosmetic for the user — it plays
  no part in Android's update logic.
- Both reach Gradle through `-PversionCode=... -PversionName=...`, read in
  `android/app/build.gradle.kts`:
  ```kotlin
  versionCode = (project.findProperty("versionCode") as String?)?.toIntOrNull() ?: 1
  versionName = project.findProperty("versionName") as String? ?: "0.1.0"
  ```
  A local build without those properties falls back to the development defaults
  (`1` / `"0.1.0"`) without breaking anything.
- **Reproducibility:** given the same commit + the same tag, the build is
  deterministic except for `versionCode`, which is intentionally tied to the
  workflow run number (not to the commit content) — it is expected, and correct,
  that re-running the same workflow (say, after a failure) produces a different
  `versionCode` than it would have before, because `run_number` advances on
  every run. `versionName` is reproducible (it depends only on the tag). This is
  the desired behavior: a `versionCode` must never be reused, even if the
  previous build failed before publishing.

## 4. ABI — nothing to decide here

A single APK covering `arm64-v8a` and `x86_64`
(`android/terminal-engine/build.gradle.kts`, `abiFilters`), with no
`splits.abi` — a policy of the Android module (Phase 4), only referenced here.
This pipeline builds whatever `:app:assembleRelease` already produces and treats
it as a single artifact.

## 5. The flow, step by step

### 5.1. Automatic (`android-release` job, `.github/workflows/ci.yml`)

Triggered only by a `git push` of an `android-v*` tag (never by an ordinary
branch/PR push):

1. Checkout of the tagged commit.
2. `./gradlew :app:assembleRelease -PversionCode=$VERSION_CODE -PversionName=$VERSION_NAME`
   — produces `android/app/build/outputs/apk/release/app-release-unsigned.apk`
   (**unsigned** — the job never had, and never will have, access to any private
   key).
3. Copies that unsigned APK to
   `data/android-release-staging/<versionCode>/app-release-unsigned.apk` on this
   same VPS (the same runner that builds it), and also publishes it as a
   workflow artifact (`actions/upload-artifact`), for whoever prefers
   `gh run download` over plain SSH.

### 5.2. Manual — offline APK signing (operator, checkpoint 1)

On the operator's machine, **never** on this VPS:

1. Download `app-release-unsigned.apk` (via `scp` from the staging path, or
   `gh run download`).
2. `apksigner sign --ks vpsmanager-release.jks --ks-key-alias vpsmanager --out app-release-signed.apk app-release-unsigned.apk`
3. `apksigner verify --print-certs app-release-signed.apk` and check that the
   `SHA-256 digest` matches `docs/android-signing-keystore.md` §5 — if it does
   not, **stop**, do not move on to the next step.
4. Copy `app-release-signed.apk` back to this VPS, into the same staging path:
   `data/android-release-staging/<versionCode>/app-release-signed.apk`.

### 5.3. Manual — offline F-Droid index regeneration (operator, same session)

Also on the operator's machine, because that is where the `repokey` lives:

1. Sync (`rsync`/`scp`) the canonical mirror of `data/fdroid/` from this VPS to
   the operator's machine, if he does not already have an up-to-date working
   copy.
2. Copy `app-release-signed.apk` into the `repo/` of that mirror.
3. Run `fdroid update` (the `fdroidserver` tool, with its config pointing at the
   operator's local `repokey`) — this regenerates and signs `index-v2.json`,
   `index-v1.jar`, `entry.json`, `entry.jar` and the icons inside `repo/`
   itself.
4. Send the whole `repo/` (now with the new APK + signed index) back to this
   VPS, into `data/android-release-staging/<versionCode>/fdroid-repo/`.

### 5.4. Automatic — publishing (`scripts/android-publish.sh <versionCode>`, on this VPS)

Runs on this VPS (the operator triggers it, over ordinary SSH or a future
`workflow_dispatch` — the script itself handles no key):

1. Checks the fingerprint of the signed APK against
   `docs/android-signing-keystore.md` §5 once more (defense in depth). A
   mismatched fingerprint = refusal, nothing is published.
2. Confirms that
   `data/android-release-staging/<versionCode>/fdroid-repo/index-v2.json`
   references the exact name of the published APK (a stale index is a silent
   failure).
3. `rsync -a --delete-after` of the package received from the operator into
   `data/fdroid/repo/` — the directory that is actually served
   (`internal/api/handlers_fdroid.go`, no redirect).

This script **never** exports anything from `data/secrets.vault`, never invokes
`fdroid update` and never sees the `repokey` — it only checks a public
fingerprint and copies already-signed bytes to the right place. Covered by TDD
in `scripts/test-android-publish.sh` (3 behaviors: mismatched fingerprint
refused; matching fingerprint published with an updated index; no key material
handled).

### 5.x — generating the incremental patches (last step, automatic)

Once the `rsync` is done, `android-publish.sh` calls
`scripts/android-patches.sh`, which generates the binary patches (HDiffPatch)
from the **already signed** APKs that were just published — the only moment this
VPS has the final bytes in hand, since signing is offline. That is why the step
comes after the fingerprint gate, not before.

A failure here does not undo the publish: the F-Droid repository is already live
and is the channel that always works; without a catalog, the app merely loses
the incremental path. Details, measured numbers and the API are in
`docs/android-incremental-updates.md`.

## 6. Who does what, where — summary

| Step | Who | Where | Needs a private key? |
|---|---|---|---|
| Build + version stamp + staging the unsigned APK | CI (`android-release`) | Self-hosted runner (this VPS) | No |
| Sign the APK | Operator | Operator's offline machine | Yes — APK master key |
| Regenerate and sign the repository index | Operator | Operator's offline machine | Yes — `repokey` |
| Verify fingerprint + publish into `data/fdroid/repo/` | `scripts/android-publish.sh` | This VPS | No |
| Generate incremental patches + manifest | `scripts/android-patches.sh` | This VPS | No |

## 7. What breaks if each key is lost

- **Losing the `repokey`:** reissuing with a new key forces **every device** to
  re-add the repository (new QR/URL). Inconvenient, not destructive — no user
  data is lost, no existing installation breaks by itself
  (`docs/android-fdroid-repo.md` §2).
- **Losing the APK master key:** no device with the app already installed ever
  receives another update — the certificate of the new APK will never match the
  installed one. There is no fix at the level of this pipeline; the only safety
  net is the backup/restore runbook in `docs/android-signing-keystore.md` §6. If
  the backup is lost too, every existing installation needs a manual reinstall —
  wiping the app's data, with no fallback on the Android side.

These two losses are **not symmetric**: the `repokey` can be reissued at the
cost of inconvenience; the APK key has no substitute that preserves update
continuity for anyone who already installed the app.
