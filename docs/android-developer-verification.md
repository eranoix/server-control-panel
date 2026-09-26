# RUNBOOK — Android Developer Verification registration

Registering the app with Google's Android Developer Verification program, under
the free **limited distribution** account.

Document created on **2026-09-05**.

## 0. Facts verified against primary sources (2026-09-05)

Before taking any action, the facts below were checked directly on
`developer.android.com/developer-verification` (Overview, Guides, "Register for
limited distribution" and FAQ pages). This corrects and sharpens what the
project's research notes said about this pitfall.

**⚠️ Google's two official pages contradict each other — this is not settled
fact, it is a divergence inside the vendor's own documentation:**

- **FAQ page** (`.../guides/faq`, "Last updated: July 15, 2026" — carries the
  channel caveat, more specific and more recent):
  > "The September 30, 2026 deadline only applies to the specific
  > participating stores. If you distribute your app through other stores, or
  > if users sideload your app directly, these new verification requirements
  > won't apply to your app yet."
  URL: `https://developer.android.com/developer-verification/guides/faq`
- **"Register for limited distribution" page** (`.../guides/limited-distribution`,
  "Last updated 2026-08-20 UTC" — a blanket statement, no channel caveat,
  chronologically more recent than the FAQ):
  > "September 30, 2026: Android developer verification protections take
  > effect. Any package names not registered by this date will no longer be
  > installable on certified Android devices in Brazil, Indonesia, Singapore,
  > and Thailand."
  URL: `https://developer.android.com/developer-verification/guides/limited-distribution`

One is channel-specific (the FAQ), the other is a blanket statement without that
caveat and is the more recently updated of the two — you cannot treat "does not
apply to F-Droid/sideload" as settled fact while the vendor itself disagrees
across its own official pages.

**Conclusion to record — the practical recommendation does not change, the
reason does:** it stops being "otherwise the app will not install after
September 30" (which would depend on accepting the FAQ page as the valid one)
and becomes **"cheap insurance against an unresolved inconsistency in Google's
own documentation, and against the 2027 global rollout"**. It is still free, it
still takes time (device pairing and review take days), and the console has its
own API for automation/CI-CD — there is no reason to wait for Google to resolve
its own contradiction.

Additional facts confirmed, with no contradiction between the pages:

- Scope of the initial enforcement (2026-09-30): Brazil, Indonesia, Singapore
  and Thailand only; participating stores only (Google Play, HONOR App Market,
  OPPO App Market, Galaxy Store/Samsung, Palm Store/Transsion, V-Appstore/vivo,
  GetApps/Xiaomi); mobile/tablet form factors only (other form factors are not
  covered in this initial phase).
- What applies globally, "regardless of your app's download source", is the
  expansion from **2027 onward** ("2027 and beyond: Global rollout for all
  certified Android devices"), with no exact date announced yet.
- Even in that global rollout, an unregistered app does **not** become
  impossible to install — the user has to go through the "advanced flow" (a
  one-time activation, with a 24-hour wait and biometric confirmation) to
  install apps from unverified developers. That is extra friction for the end
  user, not an absolute block.
- **`ADB` is never affected, at any phase** — installing/updating over ADB stays
  free of verification at every stage of the rollout.
- Account requirements for the free limited distribution account, not
  anticipated in the original plan: **two-factor authentication (2FA) is
  mandatory** on the Google account used, and a **Google payments profile must
  be linked** (used only for legal name/address — the tier itself charges
  nothing).

Sources: `https://developer.android.com/developer-verification`,
`https://developer.android.com/developer-verification/guides`,
`https://developer.android.com/developer-verification/guides/limited-distribution`,
`https://developer.android.com/developer-verification/guides/faq`.

## 1. Deadline

- Project commitment: registration confirmed **VERIFIED** before
  **2026-09-30**.
- This is an **internal target date**, not a proven hard external deadline for
  this app's distribution channel — see section 0: Google's own pages disagree
  on whether 2026-09-30 affects sideload/F-Droid, and the reason to keep that
  date is the unresolved inconsistency in the vendor's documentation plus the
  2027 global rollout, not certainty of a block on 2026-09-30.
- This document was created on 2026-09-05 — 25 days of slack before the
  2026-09-30 target.

## 2. What is being registered

- **Application ID:** `tech.northwind.vpsm.app`
  (source: `docs/android-signing-keystore.md`, section 2 — immutable after the
  first release).
- **SHA-256 fingerprint of the signing certificate:** **PENDING.**
  The single source of truth for that value is
  `docs/android-signing-keystore.md`, section 5. That document still has
  section 5 as a placeholder — the signing key has not been generated by the
  operator yet (the automated tasks ran and produced the runbook, but the
  human-action checkpoints — offline generation, custody, restore drill — are
  still pending).

  **Do not copy the fingerprint into this document once it exists.**
  When filling in section 4 below, **reference**
  `docs/android-signing-keystore.md#5-fingerprint` and read the value from there
  at submission time — keeping exactly one source of truth in the repository
  (the same value also feeds `assetlinks.json` in Phase 3, through
  `config.Config.AndroidSigningFingerprints` at runtime, not as a second static
  file).

**Current blocker:** the submission cannot be performed while section 5 of
`docs/android-signing-keystore.md` is empty. Order of execution: finish the
human checkpoints of the signing-key work (generate the keystore offline, store
the two backups, test the restore) → confirm the fingerprint in section 5 of
that runbook → only then follow the steps below.

## 3. Step by step (operator action)

Account prerequisites (confirmed on the official "Register for limited
distribution" page — they were not in the original plan, added here):

- A Google account with **two-factor authentication (2FA) enabled** — mandatory
  to create any account in the Android Developer Console.
- A **Google payments profile** linked to the account (used only for legal
  name/address; the limited distribution account charges nothing).
- A contact email (used only if Google needs to reach the operator; it is not
  made public).
- **No government ID is required** for the limited distribution account —
  confirmed in the official source.

Submission checklist:

1. Open the **Android Developer Console**
   (`https://android.google.com/developerconsole` — official guide:
   `https://developer.android.com/developer-verification/guides/android-developer-console`).
   This is the right console for anyone distributing **outside** Google Play
   (this project's case); do not use the Play Console.
2. When creating the account, explicitly pick the **"Limited distribution"**
   type (free, up to 20 devices, no government ID, pairing by QR code/link) —
   not the "Full distribution" account (paid, US$25, requires full identity
   verification).
3. Confirm that the SHA-256 fingerprint is already available in
   `docs/android-signing-keystore.md` section 5 (see the blocker in section 2
   above). If it is not there yet, **stop here** and go back to the signing-key
   work.
4. In the package name registration flow, provide:
   - Application ID: `tech.northwind.vpsm.app`
   - SHA-256 fingerprint: the value from `docs/android-signing-keystore.md` §5
     (copy it straight from there at submission time, do not retype it from
     memory).
5. Submit the registration.
6. Note the submission date and any reference/confirmation ID Google provides —
   fill them into section 4 below.
7. Keep that reference outside the repository as well (Google's confirmation
   email, for instance), at the operator's discretion.

## 4. Record (fill in after submission)

<!-- FILL IN AFTER OPERATOR ACTION -->

- Submission date: `<to fill in>`
- Google reference/confirmation ID: `<to fill in>`
- Status at submission time: `<to fill in>` (e.g. "pending" / "in review")
- Fingerprint used: see `docs/android-signing-keystore.md` §5 (do not duplicate
  the value here).

## 5. Final confirmation (fill in before 2026-09-30)

<!-- FILL IN AFTER OPERATOR ACTION -->

- Date the status was confirmed **VERIFIED** in the Android Developer Console:
  `<to fill in>`
- Where the operator stored the confirmation (screenshot/reference), outside
  this repository: `<to fill in>`
- Registration confirmed before the deadline — `<to fill in>`

If the status is still not VERIFIED as 2026-09-30 approaches, escalate/follow up
with Google support instead of waiting in silence — even given the real scope of
the deadline (section 0), this document is only considered done once VERIFIED is
confirmed.
