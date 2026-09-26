# Overridden openapi-generator templates

This directory overrides templates from `org.openapi.generator` (pinned at
`7.25.0`, see `gradle/libs.versions.toml`). The generator uses its built-in
templates for everything that is **not** here, so a file only exists here
when the original template has a proven defect.

When bumping the generator version: review each file here, check whether the
defect was fixed upstream and, if so, **delete the override** instead of
carrying a pointless divergence. Copying the new template and reapplying the
fix by hand is the worst option: it loses the record of why the override exists.

## `libraries/jvm-okhttp/api.mustache`

Byte-identical copy of the 7.25.0 template **plus one line**:
`import kotlinx.serialization.encodeToString`.

**Defect:** the `{{^multiplatform}}` block imports only `SerialName` and
`Serializable`, while the `{{#multiplatform}}` block next to it does
`import kotlinx.serialization.*`. Without the extension imported, the
`encodeToString<T>(obj)` call the template emits for each non-file form part
only sees the **two**-parameter overload
(`SerializationStrategy<T>, T`) and does not compile.

**Symptom:** dozens of
`Argument type mismatch: actual type is 'String', but 'SerializationStrategy<String>' was expected`
in `MobileApi.kt`/`WhatsappApi.kt`, breaking the whole generated client and
therefore every downstream Kotlin module. It only shows up when the spec has at
least one `multipart/form-data` endpoint.

**Not a version issue:** reproduced in isolation with
`kotlinx-serialization-json` 1.7.3 and 1.4.1 (same result on both), and the
generator's unreleased `master` has the same block. 7.25.0 is the latest release.

## `libraries/jvm-okhttp/infrastructure/ApiClient.kt.mustache`

Byte-identical copy of the 7.25.0 template **plus one guard** in `request()`:
`updateAuthParams(requestConfig)` only runs when
`requestConfig.requiresAuthentication` is `true` (search for `VPSM GUARD`).

**Defect:** the template generates the `requiresAuthentication` field on every
`RequestConfig` (derived from each operation's `security` in the spec) and then
**never reads it**: `updateAuthParams` is called on every request, including
operations that explicitly declare no security.

**Symptom in this project:** the BFF declares `bearerAuth` only on protected
routes (see `internal/mobilebff/security.go`) and the app mirrors the session
access token into `ApiClient.accessToken` (`SessionManager`, for `MediaNetwork`
and the WhatsApp WebSocket). Without the guard, `POST /auth/login`,
`/auth/refresh`, `/auth/pair` and `/auth/passkey/*`, which sit OUTSIDE
`auth.Middleware` because they happen before a session exists, would send
`Authorization: Bearer <previous session token>`. None of them reads the header
on the server, so nothing breaks, but it sends a credential nobody asked for and
diverges from `AuthTokenInterceptor` (which skips these same routes by suffix).

**Who sets the header:** protected route: the generated client sets it from
`ApiClient.accessToken` and `AuthTokenInterceptor` **overwrites** it with the
`SessionManager` token (`Request.header()` replaces, it never duplicates). The
interceptor stays the source of truth because only it knows how to refresh on
401 and retry. Public route: nobody sets the header.

## KNOWN defect NOT fixed here: read before adding multipart

The same template forces a non-null cast (`obj as kotlin.String`) on **every**
non-file form part, **ignoring whether the field is optional**. Passing `null`
for an optional part throws `ClassCastException` inside the generated method
instead of omitting the part.

This is currently worked around at the call site: `WhatsAppRepository.uploadMedia`
converts `null` to `""` before calling the generated client. That is safe for
*that* endpoint because Go's `FormValue` already returns `""` for a field that
was never sent: same request shape, no behavior change.

**This does not automatically hold for a new endpoint.** If your endpoint needs
to tell "field absent" from "field empty", the empty-string workaround is wrong
and the fix has to go here: guard the cast with a null check and omit the part.
Changing this alters the wire behavior of **every** generated multipart call, so
do it with a test that proves the difference.
