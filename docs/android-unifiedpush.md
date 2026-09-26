> # DECISION: we are staying on FCM. This document records WHY.
>
> **2026-09-05.** The operator raised the inconsistency — the app is distributed
> through its own F-Droid repository precisely to avoid the Play Store, and FCM
> put Google back on the critical path of a notification. The migration was
> researched in depth (everything below), started, and **reverted by the
> operator's decision** before any code was committed. The FCM path is intact.
>
> **What tipped the scale back:**
>
> 1. **Worse reliability on some manufacturers, failing silently.** The FCM
>    socket belongs to Google Play Services, which OEMs put on their battery
>    whitelist. A third-party distributor is not on it. On Xiaomi/MIUI, OnePlus
>    and Huawei it needs a battery exemption **plus** autostart permission —
>    without both, the process dies on reboot and **never reconnects, with no
>    error at all**. For a tool whose push carries server-down alerts, a
>    notification that silently fails to arrive is worse than depending on
>    Google.
> 2. **It adds a component, it does not remove one.** Contrary to what I said
>    before the research: `internal/webpush` is the *sender*, not the push
>    *server*. The server role (the always-on backend the ntfy app talks to) does
>    not exist in this project — it would require public `ntfy.sh` or a
>    self-hosted ntfy.
> 3. **Manual setup on every device** — installing and configuring the
>    distributor, plus the per-manufacturer exceptions.
>
> **What was given up by staying on FCM:** independence from Google, and the
> manual step of provisioning `fcm_service_account` in the vault still exists.
>
> **If this is ever reopened**, the research below still holds and saves the
> whole effort: the library was verified (Apache-2.0, artifact inspected, embeds
> Tink), the removal inventory is complete, and the concept mapping exists
> (CollapseKey→Topic RFC 8030 §5.4, Critical→Urgency §5.3). The `push` channel of
> `internal/notify` was built against an emitter interface, so a UnifiedPush
> emitter drops in without touching that file.

---

# Research — migrating from FCM to UnifiedPush on Android

Operator decision (2026-09-05): drop FCM and use **UnifiedPush** for push in the
Android app. Reason: the app is distributed through its own F-Droid repository
precisely so it does not depend on the Play Store, and FCM reintroduced a
dependency on Google. UnifiedPush 3.0 endpoints are Web Push endpoints
(RFC 8030 + RFC 8291 `aes128gcm` + RFC 8292 VAPID) — the protocol
`internal/webpush` already implements and runs in production (video call ring,
browser Web Push).

This document is research/preparation only — no app code, `libs.versions.toml` or
manifest was changed here. A sibling agent does the implementation on top of it.

## 1. The library — verified, not assumed

**Coordinate:** `org.unifiedpush.android:connector`
**Latest version:** `3.3.5` (published to Maven Central on 2026-08-22)
**License:** Apache License, Version 2.0 (confirmed in the published POM)
**Repository:** Maven Central (`repo1.maven.org`) — **no extra repository is
needed**, contrary to what the brief floated as a possibility.
`org.unifiedpush.android` resolves normally with `mavenCentral()`, which this
project already declares.

### Evidence of real incremental development

The artifact's `maven-metadata.xml` lists 27 published versions from `2.5.0` to
`3.3.5`, through `3.0.0-rc1..rc4` → `3.0.0..3.0.10` → `3.1.x` → `3.2.0` →
`3.3.0..3.3.5`. Real publication dates (from the `Last-Modified` of the artifact
itself on `repo1.maven.org`), not just version numbers:

| Version | Published on |
|---|---|
| 3.0.0 | 2024-12-19 |
| 3.1.0 | 2025-09-30 |
| 3.2.0 | 2026-01-05 |
| 3.3.0 | 2026-02-16 |
| 3.3.5 | 2026-08-22 |

Continuous development for nearly 2 years, not a single drop. The source lives
at `codeberg.org/UnifiedPush/android-connector` (the mirror at
`github.com/UnifiedPush/android-connector` exists but its releases stopped in
2021 — it is only a mirror, not the active source). The UnifiedPush project is
the same consortium behind ntfy, Gotify, ConversationsPush and others; the
connector repo specifically (split out of a larger monorepo in 2024-08) has 16
stars/7 forks on Codeberg — a low number in isolation, but the pattern of real
publication to Maven Central over nearly 2 years, maintained by the organization
(not a personal account), weighs more than stars here. This is different from the
`gopackx/open-swag-go` case (5 stars, 4 months, single author, rejected today):
there was no real version history and no organization behind it there — here
there are both.

### Confirmation from inside the artifact (downloaded and inspected, not assumed)

Downloaded `connector-3.3.5.aar` from `repo1.maven.org` and unpacked it
(`unzip` → `classes.jar` → `unzip` again → `javap -p` on the classes). Findings:

- **The AAR is not a facade `.aar`** — it has 60+ real classes, including Tink's
  own Web Push implementation
  (`com.google.crypto.tink.apps.fixed_webpush.WebPushHybridDecrypt/Encrypt`,
  `WebPushUtil`), confirming that the library implements RFC 8291
  (`aes128gcm`) itself rather than delegating to a hidden external SDK.
- **`UnifiedPush.register(Context, String instance, String messageForDistributor,
  String vapid)`** exists exactly as documented — confirmed both by `javap`
  (signature with 4 `String`s) and by reading the source (`UnifiedPush.kt` on
  Codeberg): it passes the VAPID key at registration, exactly the 3.0/Web Push
  flow the brief asked to confirm, not the legacy pre-3.0 flow (which had
  neither VAPID nor an instance).
- **`MessagingReceiver.onMessage(Context, PushMessage, String instance)`** —
  `PushMessage` arrives with `content: ByteArray` and `decrypted: Boolean`. The
  library already decrypts the payload internally before delivering it (through
  `KeyManager.decrypt`, which by default is `DefaultKeyManager` using the Tink
  classes above) — the app does not implement RFC 8291 by hand, it just reads
  already-decrypted bytes.
- **`MessagingReceiver.onNewEndpoint(Context, PushEndpoint, String instance)`**
  — `PushEndpoint(url, pubKeySet, temporary)`: the endpoint URL and the device's
  Web Push public key pair, exactly what `internal/webpush.Store` needs to store
  as a `PushSubscription` (endpoint + `keys.p256dh`/`keys.auth`).
- The AAR's `AndroidManifest.xml`: `minSdkVersion="16"`, no conflict with this
  project's `minSdk=34`. POM dependencies: `kotlin-stdlib:2.2.10` (the project
  uses Kotlin `2.4.10` — resolves without conflict) and
  `com.google.crypto.tink:tink:1.23.0` (runtime, for decryption).

**Additional artifact evaluated:** `org.unifiedpush.android:connector-ui`
(`1.1.0`, same group/license) — ready-made dialogs (`ChooseDialog`,
`IntroDialog`, `NoDistributorDialog`, `RegistrationDialogContent`) for the "no
distributor installed" / "pick a distributor" flow. **Optional**: the app already
has its own design system (`BatteryOptimizationPrompt` shows this project's
custom dialog pattern) — whether to use `connector-ui` or compose our own dialogs
from the same data (`UnifiedPush.getDistributors`, `resolveDefaultDistributor`)
is left to the sibling agent, but it is not required for the flow to work.

**No `distributor`/`distributor-base`/`distributor-ui`/`embedded-fcm-distributor`
artifact is needed** — those are for people building a *distributor* app (as ntfy
itself does), not for an app that consumes push like this one.

## 2. The distributor story — no half-truths

### What the operator installs and configures, per device (~20 devices)

1. Install **ntfy** (`io.heckel.ntfy`) from the operator's F-Droid — confirmed
   published in the official F-Droid index
   (`f-droid.org/api/v1/packages/io.heckel.ntfy`). It does not need to be in
   *this* project's F-Droid repository — it is an independent community app.
2. Open ntfy once. The first time the VPS Manager app tries to register for push
   (`UnifiedPush.tryUseCurrentOrDefaultDistributor` / `tryPickDistributor`),
   Android shows the system screen for picking a distributor — with only ntfy
   installed, the choice is automatic.
3. **If self-hosting the ntfy server** (see the decision below): inside the ntfy
   app, under Settings → "Default server", switch from `ntfy.sh` (the public one)
   to the URL of the self-hosted server. One manual step per device, once.
4. Grant "Allow notifications" (`POST_NOTIFICATIONS`, which the app already asks
   for today) — no change here.
5. Battery optimization exemption and autostart — see the caveat below.

None of these steps asks the user to copy/paste a token, endpoint or QR code —
registration between the VPS Manager app and ntfy happens through a local
`Intent`/broadcast on the device (`org.unifiedpush.android.distributor.*`), not
by hand.

### Can `internal/webpush` be "the push server" directly? — checked carefully, the answer corrects the brief

**Not the way the brief suggested, and this matters enough to call out.** There
are three distinct roles in UnifiedPush, and the brief conflated two of them:

1. **The app (VPS Manager Android)** — registers through `connector`, receives a
   `PushEndpoint.url`, and **sends** encrypted Web Push POSTs to that URL.
   **This role is already done**: `internal/webpush.Store.send()` does exactly
   that today (`webpush.SendNotificationWithContext` against `sub.Endpoint`, with
   the VAPID pair `internal/webpush` already generates and persists in
   `vapid.json`). No new component is needed here — it is direct reuse of what
   already exists for the video call "ring" and for `internal/notify`.
2. **The distributor (the ntfy app on the device)** — keeps a persistent
   connection to a "push server" and forwards the encrypted payload (which it
   never decrypts) to the app over a local broadcast.
3. **The distributor's push server** — the backend the ntfy app polls to keep
   that persistent connection (WebSocket/long-poll) and the one
   `internal/webpush` actually POSTs to. **This is the missing role**, and
   `internal/webpush` cannot take it on: it is a Web Push *sender* (a client),
   not a server that accepts `PUT`s, keeps a queue and holds a long connection
   to the phone. This is not a code limitation — it is the other side of the same
   protocol, a different role.

**Correction to the brief:** the right question is not "can the panel be the push
server", because it already fills the sender role just as before; the question is
"does the operator need to run his own ntfy server, or can he use public
`ntfy.sh` (the app's default)?" Answer:

- **`ntfy.sh` (public, the ntfy app's default) works with no new component at
  all.** Since the payload already travels end-to-end encrypted (RFC 8291, done
  on the Go side before the POST, decrypted only in the `connector` on the
  device), `ntfy.sh` never sees the content — only timing/size/topic (metadata,
  not content). Zero extra infrastructure.
- **Self-hosting ntfy** (`binwiederhier/ntfy`, Apache-2.0, a single Go binary,
  lightweight container) removes even that third-party metadata dependency, and
  is consistent with the original reason for dropping FCM (avoiding external
  dependencies). It genuinely **is a new component** (one more service to keep
  running), not the removal of one — the opposite of what the brief assumed.
  Given that the operator already runs his own VPS and already self-hosts
  everything he can (see `docs/android-fdroid-repo.md`), I recommend this option,
  but it is an operations/infrastructure decision for the operator, not something
  this agent decides alone.

To summarize for the sibling agent: **no change to `internal/webpush` is needed
for sending** — it already speaks the right protocol. The only open decision is
operational (self-host ntfy or use `ntfy.sh`), and it blocks neither the app
implementation nor the Go sender.

### The reliability caveat — said plainly

With FCM, the persistent socket that receives the push is held by the **Google
Play Services** process, which Samsung/Xiaomi/OnePlus/etc. whitelist from battery
optimization by default (it is a system process). A third-party distributor like
ntfy **does not have that privilege** — it is just another app, and on those
manufacturers its process is aggressively killed in the background, killing with
it the persistent connection that would deliver the push.

What has to be configured per device, per manufacturer:

- **All of them:** Settings → Apps → ntfy → Battery → "Unrestricted" (not
  "Optimized"). That is the equivalent of the
  `Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS` that
  `BatteryOptimizationPrompt` already requests for VPS Manager itself — the same
  prompt/educational flow should apply to ntfy as well (document it in the app's
  onboarding screen, not just ask for our own package).
- **Samsung (One UI):** besides the battery exemption, "Background data usage"
  enabled for ntfy, and "Put unused apps to sleep" disabled specifically for it
  (Settings → Device care → Battery → Background usage limits).
- **Xiaomi/MIUI:** the battery exemption is not enough on its own — it also needs
  "Autostart" enabled for ntfy (Settings → Apps → Permissions → Autostart) and
  "No restrictions" under per-app "Battery saver"; without autostart, MIUI kills
  the process when the device reboots and it does not re-register the connection
  on its own.
- **OnePlus (OxygenOS)/Huawei:** the same double requirement — battery exemption
  + manual autostart — on screens whose names vary by OS version ("Advanced
  battery management" / "App launch").

**What happens when it is not configured:** ntfy is killed minutes or hours after
going to the background; the connection drops and is not always reopened
proactively by the OS (unlike FCM, which the system prioritizes reconnecting).
The symptom is silent — no visible error, deploy/alert notifications and call
rings simply stop arriving until the user reopens ntfy by hand. This is strictly
worse than FCM on those manufacturers specifically, and it must be communicated
to the operator as an accepted trade-off, not hidden.

### No distributor installed — visible degradation, never silent

`UnifiedPush.getDistributors(context)` returns an empty list and
`resolveDefaultDistributor` returns `ResolvedDistributor.NoneAvailable` when no
distributor is present. The app has to check this during onboarding (the same
moment as `BatteryOptimizationPrompt` today) and, if empty, show an explicit
state — "push notifications require the ntfy app, tap here to open the
F-Droid/install link" — instead of silently never registering. This is a real gap
to implement (Rule 2 will still apply to the sibling agent): today the app has no
"no push provider available" path for FCM because FCM is always available through
Play Services; the UnifiedPush equivalent has to be built, it is not reuse.
`connector-ui`'s `NoDistributorDialog` covers exactly this case out of the box,
if the sibling agent chooses to use it.

## 3. Removal inventory — FCM/Firebase in the current code

Surveyed by reading every file that references `Firebase`/`FCM`
(`grep -rl -i "firebase\|fcm" android --include="*.kt" --include="*.toml"
--include="*.xml" --include="*.gradle*"`, excluding `build/`).

### DELETE (coupled to FCM, with no equivalent to keep)

| File | What |
|---|---|
| `android/gradle/libs.versions.toml` | `firebaseBom = "34.4.0"`, `firebase-bom`, `firebase-messaging` (lines 64-72, 147-148) and the comment that goes with them |
| `android/feature/notifications/build.gradle.kts` | `implementation(platform(libs.firebase.bom))` + `implementation(libs.firebase.messaging)` and the comment about "the only module that touches `com.google.firebase.*`" |
| `android/feature/notifications/src/main/kotlin/.../fcm/VpsFirebaseMessagingService.kt` | The whole class — `extends FirebaseMessagingService`, `onNewToken`, `RemoteMessage`. Becomes a new class `extends org.unifiedpush.android.connector.MessagingReceiver` (same `type`/`event_type` routing responsibility, completely different API) |
| `android/app/src/main/AndroidManifest.xml` | The `<service android:name="...VpsFirebaseMessagingService">` with `<action android:name="com.google.firebase.MESSAGING_EVENT">` (lines 42-48) — replaced by a `<receiver>` for `org.unifiedpush.android.connector.MESSAGE`/`NEW_ENDPOINT`/etc. pointing at the new class |
| `android/feature/notifications/src/test/kotlin/.../fcm/VpsFirebaseMessagingServiceTest.kt` | The whole test depends on `RemoteMessage`/`Robolectric.setupService` over the FCM class — full rewrite against the new class/API (6 `@Test`) |

### REWRITE (logic is transport-agnostic, only the transport changes)

| File | Why it is transport-agnostic | What changes |
|---|---|---|
| `android/feature/notifications/src/main/kotlin/.../fcm/ActionableNotificationBuilder.kt` | Already operates on `Map<String, String>` (the data payload), not on `RemoteMessage` | No logic change — it just receives the already-decrypted `Map` from `PushMessage.content` (parsed as JSON/query, to be decided against the real format `internal/webpush` will send) instead of `RemoteMessage.data`. The package can stay `fcm` or be renamed (cosmetic) |
| `android/feature/notifications/src/main/kotlin/.../fcm/NotificationChannels.kt` | Does not reference FCM in code, only in a comment/doc | No logic change; the comment about "before an FCM message can arrive" should become "before a UnifiedPush message can arrive" |
| `android/feature/notifications/src/main/kotlin/.../fcm/NotificationActionReceiver.kt` | Zero coupling to FCM | No change |
| `android/feature/notifications/src/main/kotlin/.../fcm/BatteryOptimizationPrompt.kt` | Zero coupling to FCM (it is about the app's own battery) | No code change; the explanatory text should mention ntfy as well (see section 2) |
| `android/data/src/main/kotlin/com/vpsmanager/data/push/PushDeviceRepository.kt` | The HTTP call logic is agnostic; only the **parameter/field name** (`fcmToken`, serialized as `"fcm_token"` on the wire) is FCM-specific | Rename to something like `pushEndpoint`/`endpoint_url` (or the full `PushEndpoint` shape: url + p256dh + auth) — this is a contract change with the backend, so the `RegisterDeviceInputBody` generated from the OpenAPI spec has to change too (outside pure Android scope; it is the client generated from the BFF spec) |
| `android/data/src/test/kotlin/com/vpsmanager/data/push/PushDeviceRepositoryTest.kt` | same as above | The two assertions that check `"fcm_token":"fcm-token-abc"` in the request body (2 `@Test`) move to the new field shape |
| `android/data/src/main/kotlin/com/vpsmanager/data/push/PushOnboardingState.kt` | Zero coupling to FCM | No change |
| `android/data/src/main/kotlin/com/vpsmanager/data/videocall/IncomingCallHandler.kt` | Pure interface, operates on `Map<String,String>`/domain `String`s (`room_id`, `call_id`) — comments mention "FCM" but the interface imports nothing from `com.google.firebase.*` | No code change; doc comments mentioning "FCM" (e.g. "hand a call-shaped FCM payload off to...") should be updated to "UnifiedPush" |
| `android/data/src/main/kotlin/com/vpsmanager/data/videocall/ActiveCallRegistry.kt` | same — only a comment mentions "FCM push" | No code change, docs only |
| `android/feature/videocall/.../TelecomIncomingCallHandler.kt` | same — comment "silently misses the ring rather than crashing the FCM-delivery path" | No code change, docs only |
| `android/feature/videocall/.../VpsmConnection.kt` | same — comment "call-ended FCM push" | No code change, docs only |
| `android/feature/videocall/src/test/kotlin/.../TelecomIncomingCallHandlerTest.kt` | A test comment mentions "FCM payload upstream"; it does not test FCM itself | No functional change, comment only |
| `android/app/src/main/kotlin/com/vpsmanager/app/MainActivity.kt` | Only imports `NotificationDeepLink` (an app class that lives under the `...fcm` *package*, not the Firebase API) | No logic change; if the `fcm` package is renamed (e.g. to `push`), the import path changes |
| `android/app/src/main/kotlin/com/vpsmanager/app/nav/AppNavHost.kt` | same | same |
| `android/app/src/test/kotlin/com/vpsmanager/app/nav/ResolveNotificationDeepLinkTest.kt` | same, only `import ...fcm.NotificationDeepLink` | No functional change; the import path changes if the package is renamed |
| `android/app/src/main/kotlin/com/vpsmanager/app/VpsManagerApplication.kt` | Imports nothing from `com.google.firebase.*`; only comments "before any FCM message can possibly arrive" | No code change, comments swap "FCM" for "UnifiedPush"; **needs new code** (not a rewrite) to call `UnifiedPush.tryUseCurrentOrDefaultDistributor`/register the `MessagingReceiver` where today nothing has to be done (the FCM `<service>` starts itself) |

**Naming note:** the Kotlin package `com.vpsmanager.feature.notifications.fcm`
currently holds 5 files that do **not** touch the Firebase API
(`ActionableNotificationBuilder`, `BatteryOptimizationPrompt`,
`NotificationActionReceiver`, `NotificationChannels`, plus the
`NotificationDeepLink` object inside `ActionableNotificationBuilder.kt`) — the
package name is FCM-specific, the contents are not. Renaming the package (e.g. to
`push`) is cosmetic but avoids the same misunderstanding that reading this
inventory nearly caused; it is the sibling agent's call.

### Tests affected — of the 412 tests today, the ones that change

Static count of `@Test` in the classes that touch FCM directly:

- `VpsFirebaseMessagingServiceTest.kt` — **6 tests**, full rewrite (the class
  under test ceases to exist; it becomes a test of the new `MessagingReceiver`
  class).
- `PushDeviceRepositoryTest.kt` — **2 tests**, a narrow change (the field name in
  the expected JSON body).

Total: **8 of 412 tests** need test-code changes. The other 3 files that mention
"fcm" (`ActionableNotificationBuilderTest.kt`, `NotificationChannelsTest.kt`,
`ResolveNotificationDeepLinkTest.kt`) only mention the package name
(`package com.vpsmanager.feature.notifications.fcm` /
`import ...fcm.NotificationDeepLink`) — zero assertion changes, and they only
need a `sed` on the path if the package is renamed.

I did not run the full `./gradlew test` suite here (out of scope for this work,
which is research/preparation only, touching no app code) — the count above is
static, from reading each listed file.

## 4. Where the brief was wrong (summary)

- **"Check whether the panel can be the push server directly"** — it cannot, and
  not because of a limitation: `internal/webpush` already fills the right role
  (sender), but the missing role (the distributor's server, holding the
  persistent connection to the ntfy app on the device) is a different role that
  no Go code in this project takes on, nor should — the ntfy server (self-hosted
  or public `ntfy.sh`) is what fills it. See section 2.
- Apart from that point, the rest of the brief matched what the research
  confirmed: `org.unifiedpush.android:connector` is the right library, resolves
  from Maven Central without an extra repository, is Apache-2.0 licensed,
  supports the 3.0/VAPID/Web Push flow natively (confirmed from inside the AAR),
  and the battery/OEM caveat is real and has to be communicated to the user the
  same way FCM already does today.
