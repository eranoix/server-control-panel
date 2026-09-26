# DEVELOPMENT signing key — this is not the release key

Generated on 2026-09-05 **on this VPS**, at the operator's request: he had no
access to his laptop and needed to unblock on-device verification.

File: `data/android-dev-signing/vpsmanager-DEV-NAO-E-RELEASE.jks`
(outside git — `.gitignore:12` covers `/data/`; mode `600`).

## Why this key can never become the release key

It was born on a **public** host. The release key has to survive a compromise of
this server — that is the entire reason `docs/android-signing-keystore.md`
requires generating it offline. This one does not survive: whoever takes over
the VPS can read it.

It is disposable **by construction**, not by agreement:

| | dev (this one) | release (future) |
|---|---|---|
| file | `vpsmanager-DEV-NAO-E-RELEASE.jks` | `vpsmanager-release.jks` |
| alias | `vpsmanager-dev` | `vpsmanager` |
| CN | `... (DEV - NAO E RELEASE)` | `tech.northwind.vpsm.app` |
| validity | 3650 days | 10000 days |
| password | `example-not-a-secret` — **not a secret** | only in the operator's password manager |

The password is public on purpose, like the `android` password of the SDK's own
debug keystore. It does not go into the vault: keeping the password of a
disposable key in the vault only teaches people to treat the vault as a junk
drawer.

## SHA-256 fingerprint

```
00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF
```

(Placeholder value — the real fingerprint of this development key is not
published here.)

It is already in `data/config.json` under `android_signing_fingerprints`, which
is a **list**. `/.well-known/assetlinks.json` serves every entry, so adding the
real key later **does not require removing this one** — during the transition,
devices signed with either key keep working passkeys.

## What it is good for

Exactly what was blocked for lack of a key — all on-device verification:

- installing on a phone and actually using the app
- the terminal memory soak test, still open
- the matrix of IME, physical keyboard, Doze, and notifications with the app
  killed
- end-to-end passkey login (`assetlinks.json` already serves this fingerprint)
- video call interop with the web client

## What it is NOT good for

- **Registering this fingerprint with Android Developer Verification.** The
  registration binds certificate + package name permanently. Registering the dev
  one means the real key could never be used with `tech.northwind.vpsm.app`.
- **Publishing an APK signed with it in the F-Droid repository.** Whoever
  installs it is stuck with this key — an F-Droid update requires the same
  signature. Switching later forces an uninstall and reinstall on every device.

Testing: yes. Distributing or registering: no.

## How to replace it with the real one

1. Generate the real one offline (`docs/android-signing-keystore.md` §3).
2. **Add** the real fingerprint to the list in `data/config.json` — without
   removing this one yet.
3. Sign and publish the release with the real key.
4. Only once every device is running a build signed with the real key: remove
   this fingerprint and delete `data/android-dev-signing/`.

The order matters. Removing the dev one before devices migrate breaks their
passkeys **silently** — `assetlinks.json` stops listing the signature they have,
and the system simply stops associating them.
