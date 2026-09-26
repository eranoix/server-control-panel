# RUNBOOK — self-hosted F-Droid repository

Custody of the key that signs the **index** of the F-Droid repository served by
this same host, plus provisioning of the tool (`fdroidserver`) that generates
that index.

## 1. Two keys, two places

This project has two completely independent signing keys, and both live
**outside** this VPS, for the same reason:

1. **APK signing key** (`docs/android-signing-keystore.md`) — lives OFFLINE, on
   the operator's machine only, generated and kept in custody since Phase 2.
   This server never had and never will have access to it, by design.
2. **F-Droid repository signing key** (this one, `repokey`, used by
   `fdroidserver` to sign `index-v1.jar`/`entry.json`) — also lives OFFLINE, on
   the same operator machine, generated with the same care. This server
   **never holds it either**: it only receives the already-signed index, and
   keeps just the public fingerprint (Section 3) as a trust anchor for the
   F-Droid client.

This is a deliberate correction to the original guidance for this work: the
initial design proposed generating the `repokey` on this very VPS and keeping
the keystore inside `data/secrets.vault`. That alternative was rejected — the
rule adopted is the same as for the APK key: no signing key for public
distribution is reachable from the host that also serves public traffic.

## 2. The trade-off this implies

Keeping the `repokey` off this host costs one extra manual step on every
publish: the index has to be signed offline and only then copied/uploaded to
`data/fdroid/repo/` on this VPS (the release pipeline is where that flow is
implemented). In exchange, you get the same guarantee as with the APK key: a
total compromise of this VPS does **not** give an attacker the ability to sign a
forged index, because the key that would do it was never here.

What a compromise of this VPS would still allow, even with the `repokey` off the
host: serving an **old** version of the repository (rollback / update denial) or
deleting files, since the HTTP handler reads straight from local disk. It would
not allow forging a new signed index, nor an APK that passes Android's signature
check (that still requires the APK key, also offline). The blast radius of
losing the `repokey` is: reissuing the repository with a new key forces every
device to re-add the repo (a new QR/URL) — inconvenient, but not destructive
like losing the APK key (which would break updates and passkeys on every
existing installation).

## 3. Where each thing lives

| Item | Location | Note |
|---|---|---|
| Repository private key (`repokey`, `.jks`/`.p12`) | Operator's machine, offline | Never touches this VPS, never `data/secrets.vault` |
| Repository key password | Operator's personal password manager | Same rule as Section 3 of `docs/android-signing-keystore.md` |
| SHA-256 fingerprint of the `repokey` (public, not a secret) | `data/secrets.vault`, key `fdroid_repo_fingerprint` | Trust anchor consumed by the install handler; see Section 4 |
| Working directory of the served repository | `data/fdroid/repo/` on this VPS | Gitignored, generated/updated at publish time, never committed |
| APK signing key | `docs/android-signing-keystore.md`, offline only | Out of scope for this document |

`fdroid_repo_fingerprint` is **not filled in** in that vault yet: the entry only
comes into being after the operator generates the `repokey` offline (Section 4)
and runs the publish command with it. Until then, the install page must treat
the absence of that key as "repository not published yet", not as an error.

## 4. Generating the repository key (operator action, OFFLINE)

Run it on the operator's own machine, never on this VPS:

```bash
keytool -genkey -alias repokey -keyalg RSA -keysize 2048 -validity 10000 \
  -keystore fdroid-repokey.jks
```

Once generated and placed in custody (same encryption/dual-copy procedure as
Section 4 of `docs/android-signing-keystore.md`, adapted to this file), get the
fingerprint:

```bash
keytool -list -v -keystore fdroid-repokey.jks -alias repokey
```

and publish **only** the `SHA256:` value (no colons, uppercase hex) into this
server's vault:

```bash
vpsmctl secrets set --user <user> fdroid_repo_fingerprint
```

Never the keystore, never the password — only the fingerprint, which is public
by nature (it is the same data the F-Droid client uses to verify the origin of
the index, analogous to the APK fingerprint in Section 5 of
`docs/android-signing-keystore.md`).

## 5. Provisioning the tool (`fdroidserver`)

`scripts/setup-fdroidserver.sh` installs `fdroidserver` into an isolated venv at
`.tools/fdroidserver-env` (it does not interfere with any other Python
environment on the machine) from the source approved by the package legitimacy
check (the official F-Droid GitLab). It is idempotent: if
`.tools/fdroidserver-env/bin/fdroid` already exists, it just confirms and exits.
The script also checks (without installing them itself, since that needs root
and is the operator's call) the required system packages:
`apksigner`, `default-jdk-headless`, `python3-pil`, `python3-pyasn1`,
`python3-pyasn1-modules`, `python3-ruamel.yaml`, `python3-yaml`.

This script handles only the tool — it does **not** generate or touch the
`repokey` (Section 4), which is always a manual, offline step.

## 6. Still pending

- **`fdroidserver` is not installed on this host** at this stage of the project
  — `scripts/setup-fdroidserver.sh` exists and was validated syntactically
  (`bash -n`), but has not been run (this avoids installing a network dependency
  without explicit approval outside this flow). Running it is the next action,
  whenever the operator decides to provision the tool.
- **The `repokey` has not been generated yet.** It depends entirely on
  Section 4, an operator action, on his own machine.
- **`fdroid_repo_fingerprint` is empty in the vault.** It is only filled in
  after Section 4.
- **The real index generation (`fdroid update`/`fdroid publish`) must run on the
  operator's machine**, against the `fdroidserver` working directory (`repo/`,
  `metadata/`, and so on, in the layout the tool expects), and only the signed
  result (`index-v1.jar`, `index-v2.json`, `entry.json`, APKs, icons) should be
  copied to `data/fdroid/repo/` on this VPS. The mechanism that copies/deploys
  that directory belongs to the release pipeline, not to this document.
