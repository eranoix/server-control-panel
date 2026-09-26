# RUNBOOK — Android release signing keystore

> **APPLICATION ID LOCKED — `tech.northwind.vpsm.app` (decided by the operator on 2026-09-05)**
>
> A deliberate, confirmed choice: the operator chose to bind the app to a
> domain he controls. The trade-offs were raised before the decision and the
> operator confirmed it anyway. Recorded here so that nobody "fixes" it later.
>
> Rejected alternatives: an ID under a domain the operator does not own, and
> `com.vpsmanager.app` (the value the module skeleton picked on its own, which
> would imply controlling yet another domain).
>
> **It becomes irreversible** once (a) the real keystore is generated
> (Section 3) and (b) the package name is registered with Android Developer
> Verification. From then on, changing it creates a brand-new app as far as the
> platform is concerned, with no migration for anyone who already installed it.
>
> The Gradle `namespace` stays `com.vpsmanager.app` on purpose: in AGP it is the
> Kotlin/R-class package, independent of the distribution identity. Renaming the
> source tree would bring no benefit and would touch every file.

Custody of the key that signs every release of the Android app. This is the most
consequential key in the whole distribution effort: there is no safety net
(Play App Signing) behind it.

## 1. Why this matters

The app is distributed outside the Play Store (self-hosted F-Droid repository +
a limited distribution account — see the project's key decisions), which means
there is **no "upload key vs. Play signing key"** split that the Play Store
gives you for free: there is exactly ONE keystore, self-managed, and losing it
or rotating it silently breaks two things at once, with no fallback from the
platform:

1. **Passkeys** — the `assetlinks.json` work trusts the SHA-256 fingerprint of
   this certificate to authorize the native app to use Credential Manager.
   A different keystore = passkey rejected with `TYPE_SECURITY_ERROR`.
2. **Updates through F-Droid** — Android requires the certificate of the new
   APK to match the installed one before it accepts an in-place update. Losing
   the keystore does not take down an installed app, but **no existing device
   ever receives another update** — the only path left would be uninstall and
   reinstall (which wipes the app's data, with no backup on the Android side).

(Full reasoning: the project's research notes on distribution pitfalls.)

That is why the private key and the passwords may **NEVER** exist on this
server (a public VPS, exposed to the internet). Generating or storing the key
here would defeat the entire point of the custody: it has to survive a
compromise of exactly this machine.

## 2. Application ID

```
tech.northwind.vpsm.app
```

Reverse-DNS of the project domain. **Immutable after the first release** — it is
the same value the Android scaffold uses for Gradle's `applicationId` and the
same one the `package_name` field of `assetlinks.json` uses. Changing it after
the first release creates, as far as the platform is concerned, a new app (no
existing device recognizes it as an update).

## 3. Generation (operator action, OFFLINE, never on this server)

Run it on the operator's own machine — never over SSH on this VPS, never in any
process that runs here:

```bash
keytool -genkeypair -v \
  -keystore vpsmanager-release.jks \
  -alias vpsmanager \
  -keyalg RSA \
  -keysize 4096 \
  -validity 10000 \
  -storetype PKCS12
```

Parameter by parameter:

- `-keystore vpsmanager-release.jks` — name of the resulting file.
- `-alias vpsmanager` — fixed alias of the entry inside the keystore; used by
  every future verification/signing command. Do not change it.
- `-keyalg RSA -keysize 4096` — RSA 4096 bits; there is no reason to use
  anything weaker than `keytool`'s own default (2048) for a key that has to
  last decades.
- `-validity 10000` — about 27 years. It has to outlive the whole project; an
  expired certificate would block any future update of the app.
- `-storetype PKCS12` — the modern format, `keytool`'s own default since
  JDK 9+ (avoids the legacy, Oracle-proprietary JKS).

When prompted, `keytool` asks for the keystore password. **Careful**: `PKCS12`
keystores (the format used here) do not support a key password different from
the keystore password — `keytool` accepts `-keypass`/a second interactive
password but **silently ignores it** and uses the keystore password for the
`vpsmanager` entry as well (confirmed by running the real command: `keytool`
prints `Warning: Different store and key passwords not supported for PKCS12
KeyStores. Ignoring user-specified -keypass value.`). In other words: there is
exactly **ONE effective password** to keep, not two. Use a strong one and:

- Keep it **only in the operator's personal password manager**.

**Never:**
- Paste the passwords into this repository, into any ticket, or into any
  chat/AI tool (including this session).
- Store the passwords in `data/secrets.vault`. That vault is for secrets THIS
  server consumes (credentials for external services); this key is the
  opposite — it has to survive a compromise of this server, so it can never be
  reachable from it.
- Run the command above on any machine other than the operator's.

The CN/organization names `keytool` asks for during generation affect neither
the fingerprint nor the behavior of the app — any value works.

## 4. Custody (operator action)

Full procedure (the same one validated, with disposable material, by the drill
in Section 6.1 — `scripts/android-keystore-drill.sh`):

1. Encrypt the `vpsmanager-release.jks` file with a backup password
   **different** from the keystore password (Section 3), also generated and
   kept only in the operator's password manager. Example command, equivalent to
   what the drill exercises:
   ```bash
   openssl enc -aes-256-cbc -pbkdf2 -salt \
     -in vpsmanager-release.jks -out vpsmanager-release.jks.enc
   ```
   (`openssl` asks for the backup password interactively — do not pass it in
   clear text on the command line).
2. Copy `vpsmanager-release.jks.enc` (never the plaintext `.jks`) to TWO
   independent locations that:
   - are not this VPS;
   - are not the same provider/account as each other;
   - are under the operator's exclusive control (for example: an offline
     safe/encrypted drive + cloud storage encrypted client-side before upload,
     such as Backblaze B2 or similar).
3. Verify each copy at backup time (before considering the copy valid): run
   `sha256sum vpsmanager-release.jks.enc` at the source and, after copying, run
   `sha256sum` again at the destination — the hashes of the encrypted file (not
   to be confused with the certificate fingerprint of Section 5) must match
   byte for byte, confirming the copy was not corrupted in transit.

Record here, once the key exists, **only the names of the locations** (never
credentials, never paths that reveal how to reach them):

- Copy A: `<PENDING — fill in after the key is generated>`
- Copy B: `<PENDING — fill in after the key is generated>`

Neither the plaintext `.jks` nor the passwords ever enter this repository. The
encrypted `.jks.enc` does not either — it lives only in the two locations above.

## 5. Fingerprint

```
SHA-256: <PENDING — fill in after the key is generated>
```

Verification command to obtain/recheck the value (always run it against the
operator's machine, never copy the `.jks` to this VPS for it):

```bash
keytool -list -v -keystore vpsmanager-release.jks -alias vpsmanager
```

The line starting with `SHA256:` is the value to record above.

This is the **only** artifact derived from the keystore that becomes public.
See Section 7 for where that value is read from here (single source, no
independent copies drifting apart).

## 6. Tested restore

A backup that was never tested is not a backup — that is exactly the failure
mode the pitfall notes describe. Two parts: 6.1 has already been run and proves
that the *procedure* (commands, flags, fingerprint format, restore path) works,
using 100% disposable material; 6.2 is the operator running that same procedure
against a real backup, and it is what is still missing to close this section.

### 6.1. Procedure validated with a disposable keystore (done)

`scripts/android-keystore-drill.sh` runs end to end inside a `mktemp -d` that is
deleted on exit (`trap ... EXIT`):

1. generates a keystore with the same security parameters as Section 3 (RSA
   4096, 10000-day validity, PKCS12, alias `vpsmanager`), with throwaway
   passwords passed through environment variables (`-storepass:env`/
   `-keypass:env`, never literals in shell arguments);
2. encrypts that disposable keystore (`openssl enc -aes-256-cbc -pbkdf2`),
   exactly as Section 4 requires for the real one;
3. restores the encrypted copy into another directory;
4. runs `keytool -list -v` on the original and on the restored copy, extracts
   the `SHA256:` line from each, normalizes them and compares them
   programmatically (`test "$a" = "$b"`);
5. exits non-zero if the fingerprints differ, if `keytool`/`openssl` is
   missing, or if any step fails.

Recorded run (2026-09-05, JDK 21) — fingerprint values below are a placeholder
example, not the output of any real run:

```
original fingerprint  : 00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF
restored fingerprint  : 00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF
OK: generation + encrypted backup + restore + fingerprint check all matched.
```

That fingerprint belongs to a disposable key, generated and deleted in the same
session — it has no relationship to the real key of the app and must not be
reused for anything.

Re-run it at any time (safe, leaves no trace, uses no real material):

```bash
scripts/android-keystore-drill.sh
```

### 6.2. Drill with a real backup (operator action)

Take ONE of the two copies recorded in Section 4 (not the original working
copy) and restore it into a scratch directory on the operator's own machine:

```bash
openssl enc -d -aes-256-cbc -pbkdf2 \
  -in vpsmanager-release.jks.enc -out /scratch/path/vpsmanager-release.jks
keytool -list -v -keystore /scratch/path/vpsmanager-release.jks -alias vpsmanager
```

Confirm that the printed `SHA256:` line is **identical** to the one recorded in
Section 5. Then delete the scratch file.

Drill record:

- Date: `<PENDING — fill in after the drill>`
- Copy tested: `<PENDING — fill in after the drill>` (e.g. "copy B — Backblaze")
- Command run: `keytool -list -v -keystore <restored> -alias vpsmanager`
- Result: `<PENDING — fill in after the drill>` (fingerprint matched / did not match)

If the fingerprint does not match, that is a serious failure to fix
immediately — not a note for later.

## 7. Single source of the value (keeps consumers from drifting)

The application ID (Section 2) and the SHA-256 fingerprint (Section 5) are
consumed in at least three places: the Google Developer Verification
registration (`docs/android-developer-verification.md`), the
`/.well-known/assetlinks.json` served by the backend, and the Gradle
signing/verification configuration. Copying the value by hand into three places
is exactly how a fingerprint drifts silently — passkeys break without showing
up in any review.

**Single source, defined:** this document (Sections 2 and 5) is the *human*
origin of the value — it is born here, as soon as the operator generates the key
and runs the restore drill. From here on:

- **Backend (`assetlinks.json`):** the value lives in `data/config.json`, fields
  `android_package_name` and `android_signing_fingerprints` (struct `Config` in
  `internal/config/config.go`), read at runtime by the handler — not compiled
  into the binary. Populating those fields (by editing `data/config.json`
  directly and restarting `vps-manager`, since there is no `vpsmctl config set`
  yet) is the step that materializes the value of Section 5 in the production
  system. **That file (`data/config.json`) is the runtime source of truth** —
  any other consumer must read from it rather than keep an independent copy.
- **Developer Verification registration:** the value is copied straight from
  Sections 2 and 5 of this document into Google's console (there is no API), and
  the copy is recorded in `docs/android-developer-verification.md` as dated
  evidence — it is not a second source, it is a one-off transcription of
  something that never changes afterwards.
- **Gradle / signature verification — recorded decision:** `data/config.json` is
  a runtime file, outside git (`.gitignore` anchors `/data/` at the root),
  generated on the host that serves the backend — it does not exist in the
  checkout CI uses to build the APK (self-hosted runner, but its own checkout,
  not the production install directory). Reading that file at Gradle build time
  is not viable today. The canonical build-time copy is
  `android/gradle.properties`, key `vpsmanager.applicationId` — the build's only
  literal (`app/build.gradle.kts` reads that property instead of hardcoding the
  value). To keep that copy and this Section 2 from drifting silently,
  `app/build.gradle.kts` registers the `verifyApplicationIdMatchesDocs` task
  (wired to `preBuild`, so it fires on `./gradlew build`, `assembleDebug`,
  `assembleRelease`, and so on): it reads the code block of this Section 2 and
  fails the build with a `GradleException` if the value does not match
  `vpsmanager.applicationId`. `data/config.json` remains the runtime source for
  `assetlinks.json` (`internal/api/handlers_wellknown.go`) — the operator
  populating that field manually from this Section 2 is still the step that
  connects them, with no fourth independent value.
