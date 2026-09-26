# Technical record — the VPS Manager Android app

> **What this document is.** The complete record of the native Android app:
> where it came from, how it was built day by day, what each piece is made of,
> what each screen does, what broke along the way, and why every structural
> decision is the one it is.
>
> **Method.** Every statement here was read from the code, from the git
> history, from the planning records, or measured on this machine. Where
> something is an estimate, it says so.
>
> **Last revised:** 2026-09-08 · published version **0.1.27** · 260 production
> sources and 171 test sources.

---

# Contents

**Part I — Origin and history**
1. What came before, and why it died
2. The decision: native, and what "native" rules out
3. The plan: 12 phases
4. The build, day by day
5. The defects that shaped the app

**Part II — How it is made**
6. Architecture overview
7. The contract with the server — including what happens with no network (§7.5)
8. Identity, session and security

**Part III — What it does**
9. The screens, one by one
10. Inside the terminal

**Part IV — Life cycle**
11. Updating the app itself
12. Build, signing and distribution
13. Tests
14. Version history
15. Locked decisions
16. Where to read next

---

# Part I — Origin and history

## 1. What came before, and why it died

VPS Manager is a web panel served by a single Go binary: 380 HTTP routes, 17
WebSocket endpoints, ~40 navigation sections, an Alpine.js SPA embedded in the
binary. It already worked in a phone browser.

**Working and being usable are not the same thing in a terminal.** The case
that settled it: driving a shell from the Android browser. The soft keyboard
resizes the viewport; `xterm.js` rebuilds the grid on every animation frame;
the terminal's text selection fights the browser's; the app is killed when it
goes to the background and the session drops; and there is no `Esc`, `Ctrl` or
`Tab` on a soft keyboard.

There was an earlier attempt: a `/m/` build of the panel — parallel screens,
hand-made for the phone. It died, and the cause of death is the only one worth
recording, because it is structural and not a matter of effort: **it diverged
from the main panel on every change.** New functionality was born in the
panel; `/m/` fell behind; the distance became debt; the debt became
abandonment.

That is the risk the native app had to remove before anything else. It attacks
it from two sides at once:

- **A single contract** (the BFF, with a generated client) makes divergence
  *structurally hard*: changing the server without regenerating the spec breaks
  the app build.
- **Server-Driven UI** keeps the long tail from being rewritten: the admin
  screen lives on the server, and a new feature shows up in the app with no
  release.

## 2. The decision: native, and what "native" rules out

Decided before the first line, and not re-derived since:

**Kotlin + Jetpack Compose, with no browser engine at all.** No TWA, no
WebView, no Capacitor, no Tauri. The terminal is the app's reason to exist, and
a terminal inside a WebView inherits exactly the problems that motivated
leaving the browser.

What is **out of scope by design** — not for lack of time:

- The panel tabs that are literally a browser: Ultraviolet, code-server, the
  Grafana embed, `/_docs`. Their flows are covered by the native terminal,
  editor and charts.
- Game-server sections (plans 08-04 and 08-05, cancelled by the owner on
  2026-09-05 before a single line was written).

Alternatives evaluated and discarded, with the reason:

| alternative | why not |
|---|---|
| Connect RPC (instead of OpenAPI) | `connect-kotlin` was at v0.9.0 with dependabot-only activity |
| RemoteCompose | `androidx.compose.remote` at alpha16 — stays on the radar, not on the critical path |
| `alacritty_terminal` + `vte` | plan B for the emulator; libghostty-vt is MIT, C ABI, zero dependencies |
| ML Kit for the QR | would drag in Play Services as a transitive dependency just to read a QR code |
| `dataSync` *foreground service* | Android 15 kills it after 6 h and the type is being deprecated — and it is unnecessary, the session lives on the server |

## 3. The plan: 12 phases

The roadmap was built to **remove risk early**, not to
deliver value in order of ease. Two server-only tracks start in parallel with
Phase 1, because neither depends on any Android code existing.

| # | phase | goal |
|---|---|---|
| 1 | Foundation & Contract | BFF spine, Kotlin client generated from OpenAPI, module skeleton, CI gate against calls outside the BFF |
| 2 | Push channel & Distribution registration | `push` channel in `internal/notify`; keystore and developer verification before 2026-09-30 |
| 3 | Authentication chain | passkey + QR pairing, TOTP recovery, RBAC-filtered app, `assetlinks.json` |
| 4 | Terminal engine spike | prove ownership of the `InputConnection` and the native VT engine **before** building anything on top |
| 5 | Terminal vertical slice (live) | the real terminal: resume a session, reconnect, select/copy/paste, scrollback, extra keys, physical keyboard |
| 6 | Real-time operations | **one** multiplexed socket feeding push, live deploys, health/queue/alerts |
| 7 | SDUI tooling + pilot screen | closed vocabulary of 7 components, versioning, unknown-type fallback, contract-diff CI, proven on a real screen |
| 8 | SDUI fan-out | the rest of the admin sections, with server-side definitions only |
| 9 | WhatsApp | read, reply and send media from the phone |
| 10 | Files & Editing | browse, view, edit, transfer and share-to |
| 11 | Video call | join/receive with a native call UI and ringing on the lock screen |
| 12 | Distribution | own F-Droid repository, automatic updates, publishing pipeline |

**The order of the first two phases is the whole point.** Phase 1 exists to make
divergence structurally hard; Phase 4 exists to prove the terminal's input
model *before* building eleven features on top of it — it was exactly the dual
ownership of text (IME × terminal content) that brought down the earlier
attempt.

Two course corrections were recorded in the roadmap itself, instead of being
erased:

- **Phase 2, criterion 1** said "FCM call". Corrected on 2026-09-05: FCM is
  unreachable at that phase (there is no app and no device token yet), so the
  criterion became **Web Push**, with the channel built against an emitter
  interface so that Phase 6 can add the FCM emitter without touching Phase 2
  files.
- **Phase 2, criterion 3** treated 2026-09-30 as a hard external deadline.
  Google's own documentation contradicts itself: the FAQ says the requirement
  "only applies to participating stores… if users install your app directly,
  these requirements still do not apply"; the more recent limited-distribution
  page states without that caveat that unregistered packages "will no longer be
  installable on certified Android devices in Brazil…". This app is
  distributed through its own F-Droid repository, not through a participating
  store. The date was kept as an **internal target** — registration is free and
  removes the doubt.

## 4. The build, day by day

**The whole app was built in four days.** 348 commits touching `android/`,
`internal/mobilebff/` and `internal/androidupdate/`:

| day | commits | what happened |
|---|---:|---|
| **2026-09-05** | 237 | from nothing to a feature-complete app |
| **2026-09-06** | 94 | hardening: tests, field fixes, automatic updates, visual identity |
| **2026-09-07** | 12 | the hunt for the terminal duplication |
| **2026-09-08** | 5 | auto-update unblocked, keyboard, conversation history |

### Day 1 — 2026-09-05: the entire foundation

The real order of the commits tells the story better than any summary. It
started with the **contract**, not the screen:

1. `feat(01-01)` — the spike of the `Go struct tags → OpenAPI → Kotlin client`
   path, proven on a throwaway route before any real route.
2. `feat(android)` — minimal Gradle project with the generated client.
3. `feat(mobilebff)` — the first real route, `GET /me`, **adapting**
   `internal/auth` instead of duplicating it.
4. `feat(android)` — full skeleton of the module graph.
5. `feat(android)` — **the gate**: the build refuses `android.*`/HTTP imports in
   `:core`.

Only then came UI. The same day brought in, in parallel: the 7-type SDUI
vocabulary and the reflection-based contract generator; the screen registry
with RBAC filtering **by omission** (what the user may not see is not sent, not
hidden in the client); the libghostty-vt JNI shim with *lock-then-copy*; the
minimal `InputConnection`; the Canvas renderer with a glyph atlas; the
terminal, files, WhatsApp and transfer routes; passkey and QR pairing; the
event socket; the sora-editor with TextMate; the licenses screen.

Two details from that day that paid for themselves later:

- `feat(android-foundation): closes the network gate by type resolution
  (detekt)` — the regex gate had a bypass through string concatenation and
  fully qualified references. It was closed by type analysis, not by text.
- `test(terminal): corpus of 11 VT fixtures with cell-level and pixel-level
  conformance` — proving the renderer **without a device**, against goldens.

### Day 2 — 2026-09-06: the app meets reality

This is the day the app went from "compiles and passes the tests" to "runs on a
device". Most of the commits are fixes for things that only appear when the app
actually opens:

- `fix(android): the app no longer dies at boot on an initialization failure,
  and reports what broke` — the **Diagnostics** screen is born, the only
  channel through which information leaves a device without `adb`.
- `fix(android): falls back to unencrypted preferences if the Keystore fails on
  first boot`.
- `fix(android): the whole SDUI fell into "This section does not exist"` — the
  BFF prefix went into the URL **twice**.
- `fix(android): the SDUI screen crashed the app when the table had rows` —
  nested vertical scrolling.
- `fix(terminal): text came out with gaps` — the atlas rasterized in Roboto,
  not in the monospaced face.
- `fix(terminal): the grid painted over the rest of the screen` — `Canvas` with
  no clipping.
- `fix(terminal): tapping the grid did not open the keyboard — and there was no
  way back`.
- `fix(terminal-engine): stops leaking an ART global reference and swapping the
  buffer under the snapshot` — the classic JNI-boundary defect.
- `fix(benchmark): both throughput benchmarks could never run — and what they
  measured was not the renderer`.

And three structural changes:

- `build(android): turns on R8 for release — a 70 MB APK drops to 29 MB`, plus
  `build(android): filters ABIs down to arm64-v8a and x86_64, removing 18 MB of
  dead weight`.
- `feat(android): replaces the eight-destination bottom bar with a grouped
  drawer` — eight items do not fit a Material 3 `NavigationBar`, which is
  specified for three to five.
- `feat(android): Admin gets a section picker — 25 reachable screens, not one`
  — the drawer item pointed at a section hard-coded in the client
  (`scheduler.jobs`) while the server was already serving 25.

At the end of the day the entire **incremental update channel** landed:
manifest, resumable byte route, `:patch-engine`, data layer, the fallback
ladder and the banner.

### Day 3 — 2026-09-07: the hunt for the duplication

Twelve commits, one subject: **the terminal history duplicated when the
keyboard opened and closed**. The sequence shows three wrong hypotheses before
the right one — and it is worth keeping, because it is the record of how the
cause was found:

1. `fix(terminal): attach duplication and an option for how many history lines`
   — hypothesis: it is the attach replay. **Wrong.**
2. `fix(terminal): the duplication was the keyboard animation, 20 SIGWINCH per
   tap` — instrumentation found the real cause.
3. `fix(terminal): removes the history clear on attach — that is what froze the
   drag` — fix #1 had created a worse defect: it wiped the entire scrollback,
   and the drag "only came free when the keyboard appeared".
4. `fix(terminal): the grid stops shrinking with the keyboard` → **`Revert`** →
   **`Reapply`** → `wip(terminal): DO NOT publish before replacing the
   heuristic with the inset measurement` → `fix(terminal): the grid stops
   shrinking with the keyboard — no heuristic`.

That back-and-forth has a name: the first version **remembered** the tallest
height ever seen, and intermediate frames of a navigation transition produced
spurious measurements that a maximum keeps forever. Result on the owner's
device: **a blank screen in 0.1.17**. The final version remembers nothing — the
height is recomputed every frame from the IME *inset*.

### Day 4 — 2026-09-08: unblocking what stood in the way of shipping

- `fix(update): auto-update stuck on "Self update is blocked by unknown source
  package"` — the loop the owner was caught in eight times.
- `fix(terminal): the keyboard stops clipping the bottom line of the input box`
  — the cursor anchor needed two lines of slack.
- `feat(terminal): visible-lines control; the history panel moves out of the
  menu`.
- `feat(terminal): the conversation history is now replayed, not guessed`.
- `fix(terminal): separates "do not send me the history" from "this is not a
  fresh attach"`.
- `feat(android): update from within the app, and a button to check now` — the
  owner stops downloading APKs by hand.
- `feat(admin): Administration opens on a launcher, with search and a command
  palette` — 24 of the 25 screens the server was already serving were
  unreachable.
- `fix(terminal): the app asserts the grid size again on every connection` —
  the blank screen that healed itself (§5.7).
- `feat(offline): the app stops dying without internet, across all 72 routes at
  once`.
- `feat(offline): a banner that keeps the cache from lying, and the write
  queue`.

## 5. The defects that shaped the app

These are not "bugs fixed": each one changed an architecture decision, and all
of them are pinned by a test or by an invariant.

### 5.1 The history duplication (0.1.12 → 0.1.20)

**Symptom:** the same block of conversation appeared two, three, many times
when the keyboard opened and closed.

**Cause, measured in the server log.** A single tap to open the keyboard
produces *ten* grid-size changes — one per animation frame — and another ten on
close:

```
resize do cliente: 67x46 -> 67x42
resize do cliente: 67x42 -> 67x39
... 39, 38, 36, 35, 34, 33, 32
resize do cliente: 67x31 -> 67x30
```

Each one becomes a `SIGWINCH`, and a differential renderer (Ink, from Claude
Code) repaints the **entire frame** on every `SIGWINCH`. A frame taller than
the screen **cannot erase itself**: the repaint's `ESC[nA` saturates at the
first line of the SCREEN and never reaches the scrollback. Twenty repaints,
twenty copies.

**Fix, in three layers:** a 220 ms debounce (only the size at which the grid
*settles* gets sent); the grid does not shrink with the keyboard (it is
shifted, not re-measured); and the server's `repaint-wobble` stopped touching
**columns**.

That last one deserves its own note. The wobble shrank the grid to force the
repaint, and shrank `Cols: c / 2` along with it. The service log records:

```
[pty] repaint-wobble no attach: 49x37 → 24x18 → 49x40 (sessão "Aplicativo")
```

24 columns is exactly the width of the squeezed text in the screenshot the
owner sent, and the timestamp matches. Width is **content**, not screen
geometry: dropping to 24 columns made the program re-render the whole
conversation wrapped at 24, and coming back re-rendered everything again at 49
— both versions stay. Worse: all of this was written into the session log, so
**every future attach re-emitted that garbage**. The "Aplicativo" log had 68
stretches of width 24 and 49 of width 23, sediment from old wobbles that no
terminal can reflow afterwards (the break is the program's own `\r\n`, not
terminal wrapping).

### 5.2 The blank screen in 0.1.17

**Symptom**, in the owner's words: *"you broke the terminal for good, nothing
shows up for me any more"*.

**Cause:** the geometry fix kept the tallest height ever seen. During a
navigation transition the measurement arrives wrong (`1080x1621px` mid
transition, 5 samples out of 5) — and a maximum keeps an error forever.

**Fix:** `GeometriaDaGrade` became an `object` with **no state at all**. The
full height is `height + max(0, ime − navigationBar)`, recomputed every frame.
Memory was the defect; the absence of it is the fix.

### 5.3 The keyboard clipping the input box

Anchoring the grid to the end of the frame left a blank screen in a session
with little content (5 lines at the top of a 46-line grid → 27 empty visible
lines). Anchoring to the cursor solved that, but Claude Code's input box is
**three lines with the cursor in the middle** — aligning the cursor with the
bottom of the screen ate the lower border. Hence
`LINHAS_ABAIXO_DO_CURSOR = 2`.

### 5.4 The "self update" loop

**Symptom:** `INSTALL_FAILED_ABORTED: Self update is blocked by unknown source
package`, eight times in a row.

**Cause:** Android refuses an install session that declares itself as the same
package (`setAppPackageName`) when `installerPackageName` is null — the case
for every hand-installed APK. The recorded installer cannot be set without the
privileged `INSTALL_PACKAGES` permission.

**Fix, in two steps:** the app only declares itself when
`getInstallSourceInfo().installingPackageName` exists; and, if the session is
refused anyway, the **already verified** APK is handed to Android's standard
install screen. Without that second step, one refusal turns into an infinite
"try again" loop.

> **Manifest trap** found here: three modules declare a `FileProvider`, and the
> Android *merger* keys `<provider>` by `android:name`. Each one needs its own
> **subclass**. `tools:replace` would "solve" it by silently deleting one of
> them.

### 5.5 The history that could not be text

**Symptom:** the owner could not scroll back and reread the conversation.

**Three limits, and the third invalidated the whole solution:**

1. The server's replay on attach is capped at 128 KiB and does not reach
   generation `.1` of the rotation.
2. For his working session, the replay returned **zero**: 3,830 repaints
   (`ESC[nA`) in the last 256 KiB trip the `fluxoERepintado` guard.
3. The panel that fetched the rest asked for **text with the ANSI stripped** —
   and that could not work. Measured on the real log: the last 5,000 lines of
   plain text had **511 non-empty lines and 150 distinct ones**, almost all of
   them *spinner* frames. A program that redraws rewrites the SAME cell
   hundreds of times; without the escapes, each rewrite becomes a line and the
   text comes out shredded (`'*lg'`, `'Ml'`, `'✢i…'`). The information that
   would say where each piece goes is exactly the information that was
   stripped.

**Fix:** the server stopped trying to render and started delivering the **raw
bytes**; the app replays them into its own libghostty-vt. Same log: **5,058
readable lines** against 511 shredded ones.

### 5.6 The half-finished publish

**Symptom:** the update did not show up in the app; the only way out was to
download the APK by hand.

**Cause:** publishing happens in **two** places — the F-Droid repository
(`data/fdroid/repo/`, which serves the file) and the incremental channel
(`data/android-updates/`, which is what the **app** queries). The script in use
was a draft living outside the repository that only did the first.

**Fix:** `scripts/android-publicar-devsigned.sh`, which does both and does not
exist outside the repository. Two traps found while writing it, both because
they happened on the first run:

- the target is the **served** `data/` (`VPSM_HOME`), never the one in
  whichever worktree you happen to be in — each ticket works in its own
  worktree with a genuinely empty `data/`, and a relative path publishes with a
  success exit code into a directory that serves nothing;
- the index has to keep a **window** of versions, otherwise `android-patches.sh`
  produces only the full artifact and every update becomes a 16 MB download
  instead of a 7 MB patch.

### 5.7 The blank screen that healed itself

**Symptom**, with a photo: the grid opened blank or with the drawing broken,
and *"when I start typing it goes back to normal"*. That detail — recovering on
typing — is what pointed at the cause: it was not the drawing that was wrong,
it was the **size**.

**Cause**, read in the server log and not deduced: when it accepts a client,
`internal/pty` does a *repaint-wobble* — it changes the PTY size for one frame
and back, to force the remote program (Claude Code, a TUI) to redraw itself
entirely. It exists for a real reason: **the session log only records while a
client is attached**, so without the repaint there is nothing to replay. But
the client only sent a `resize` when **its own** size changed, and reconnecting
does not change the phone's size. Result: server and client disagreed about the
width, and the remote program drew for a grid that did not exist. Typing made
the program redraw itself, and the screen "fixed" itself — masking the defect.

**Fix:** `TerminalSocketClient` keeps the last asserted grid and **re-asserts it
on every `onOpen`**, not only when it changes. The test was written to fail
without the fix before the work was called done, and there is an invariant in
the gate.

### 5.8 The idempotency header that never existed

This one never became a production defect because it was found during
verification — and it is the most instructive of the eight.

The first version of the write queue (§7.5) sent an `Idempotency-Key` header on
every submission, and the text alongside it confidently explained why that made
the retry safe. Then the server was read: **the BFF has no generic support for
that header.** Idempotency exists on exactly one path, sending a WhatsApp
message, via `client_msg_id` in the body. On every other route the header would
be silently ignored.

This matters because **a timeout is indistinguishable from "it never
arrived"**. A queue that resends against a route with no idempotency can
restart the same container twice — and the app would have promised, on screen,
that the action was saved.

**Fix:** `enfileirar` now **requires** the caller to declare the proof
(`ProvaDeIdempotencia`), **with no default value**. A default would make the
question skippable, which is exactly how an action becomes a duplicate. Honest
and recorded consequence: today the queue can only carry the WhatsApp send;
opening it up to the rest is a **server** change, not a client one.

---

# Part II — How it is made

## 6. Architecture overview

```
   ┌───────────────────────────── device ───────────────────────────────┐
   │                                                                    │
   │   :app            navigation, theme, update banner, lifecycle,     │
   │     │             FCM, diagnostics                                 │
   │     ├── :feature-terminal   :feature-admin   :feature-files        │
   │     │   :feature-whatsapp   :feature-videocall                     │
   │     │   :feature-auth       :feature-notifications                 │
   │     │                                                              │
   │     ├── :sdui              renders screens sent by the server      │
   │     ├── :design-system     Material 3 theme, state colors, icons   │
   │     ├── :terminal-engine   JNI over libghostty-vt (C ABI)          │
   │     ├── :patch-engine      JNI over hpatchz (HDiffPatch)           │
   │     ├── :core              models and utilities, no Android        │
   │     └── :data              ALL networking: repositories, session,  │
   │           └── :data:mobile-api-client   GENERATED OpenAPI client   │
   └────────────────────────────────┬───────────────────────────────────┘
                                    │ HTTPS + WebSocket
   ┌────────────────────────────────┴───────────────────────────────────┐
   │  internal/mobilebff   — Go BFF, prefix /api/mobile/v1              │
   │  internal/pty · auth · notify · whatsapp · videocall · docker …    │
   └────────────────────────────────────────────────────────────────────┘
```

**The rule that structures everything:** only `:data` knows about the network.
No feature module imports `MobileApi`, `OkHttp` or the generated types; they
all see nothing but interfaces and sealed results. The gate has two layers,
because the first one was bypassable: a regex at the Gradle level **plus** a
Detekt rule with type resolution (`BffOnlyNetworkClientRule`), which closes the
bypass through string concatenation and through fully qualified references.

### 6.1 Size

| module | `main` sources | test sources |
|---|---:|---:|
| `:app` | 15 | 13 |
| `:core` | 11 | 4 |
| `:data` | 69 | 42 |
| `:sdui` | 14 | 7 |
| `:design-system` | 8 | 4 |
| `:terminal-engine` | 5 | 4 |
| `:patch-engine` | 2 | 4 |
| `:feature-terminal` | 63 | 42 |
| `:feature-files` | 16 | 9 |
| `:feature-whatsapp` | 15 | 10 |
| `:feature-videocall` | 12 | 9 |
| `:feature-auth` | 10 | 9 |
| `:feature-notifications` | 9 | 6 |
| `:feature-admin` | 10 | 6 |
| `:benchmark` | 1 | 2 |
| **total** | **260** | **171** |

### 6.2 Platform and libraries

| item | value | why |
|---|---|---|
| `minSdk` | 34 | fleet confirmed 100% Android 14+; the native Credential Manager already exists in the framework from here on |
| `targetSdk` / `compileSdk` | 36 / 37 | |
| Kotlin / AGP | 2.4.10 / 9.3.0 | |
| UI | Compose (BOM 2026.08) + Material 3 | |
| Network | OkHttp 4.12 + kotlinx.serialization | via the generated client |
| Background | androidx.work | resumable transfers, update checks |
| Local secrets | `EncryptedSharedPreferences` (Keystore AES256-GCM) | |
| Editor | sora-editor 0.24.4 (LGPL-2.1) | tree-sitter; forces the Licenses screen |
| Video | media3 1.11 | WhatsApp media |
| WebRTC | `io.getstream:stream-webrtc-android` | |
| QR | CameraX 1.6.2 + zxing-core 3.5.4 | pure-JVM zxing instead of ML Kit |
| NDK / Zig | r27d / 0.15.2 | Zig read from the `build.zig.zon` of the pinned commit |
| ABIs | `arm64-v8a`, `x86_64` | the second one is the emulator |

**No Google Play Services dependency** except FCM. The app works on a device
with no store.

## 7. The contract with the server

### 7.1 The BFF

The web panel has 380 routes, designed for an SPA that makes many small calls —
the opposite of what a phone wants. The app talks to its own **Backend For
Frontend**, `internal/mobilebff`, under `/api/mobile/v1`, with **72 routes** and
75 operations.

The BFF almost never contains business logic: each handler is a thin wrapper
over `internal/pty`, `internal/auth`, `internal/docker`. New logic appearing
there is a sign of scope leaking.

### 7.2 How the contract becomes code

```
      Go struct tags              huma (OpenAPI 3)         openapi-generator
internal/mobilebff/*.go  ──►  mobile-v1.yaml  ──►  :data:mobile-api-client (Kotlin)
       │                          │                        │
   `make mobile-openapi-spec`   committed in the repo   generated in the Gradle build
```

The generated client uses `withContext(Dispatchers.IO)` on every call, so
repositories can be invoked from any dispatcher without blocking the UI. One
detail that cost a fix: the spec had to **declare the bearer JWT**, otherwise
the Kotlin client was born with authentication dead.

### 7.3 The routes, by subject

| subject | routes |
|---|---|
| authentication | `/auth/login`, `/auth/logout`, `/auth/refresh`, `/auth/pair`, `/auth/passkey/{register,login}/{begin,finish}`, `/me` |
| terminal | `/terminal/sessions`, `/terminal/ws-ticket`, `/terminal/scrollback`, `/terminal/log-bruto`, `/terminal/sessions/rename`, `/terminal/backups`, `/terminal/backups/restore`, `/terminal/backups/{id}` |
| SDUI screens | `/screens`, `/screens/{id}`, `/actions/{action_id}` |
| system | `/system/{metrics/cpu,metrics/mem,metrics/disk,ports,processes,units,history}` |
| docker | `/docker/{containers,images,volumes,networks,compose}` |
| operations | `/ops/status`, `/ops/deploy`, `/ops/deploy/{jobID}`, `/deploy/apps`, `/queue/jobs`, `/scheduler/jobs`, `/alerts/rules` |
| security | `/security/{users,sessions,devices,audit,secrets,ufw/status,adguard/status,economia}` |
| files | `/files/{list,read,write,download,inbox}`, `/files/upload/{init,chunk,complete}` |
| WhatsApp | `/whatsapp/chats`, `/whatsapp/chats/{jid}/{messages,media,read,avatar}` |
| video call | `/videocall/rooms`, `/videocall/ws-ticket` |
| notifications | `/notify/devices`, `/notify/devices/{id}`, `/notify/preferences` |
| Jira | `/jira/issues`, `/jira/issue-detail`, `/jira/issue-transitions` |
| update | `/app/update`, `/app/update/artifact` |
| events | `/events/ws-ticket` |

### 7.4 WebSockets

Four kinds of socket. **None of them carries the JWT in the URL**: the app asks
for a single-use *ticket* (60 s) through the matching REST route and presents it
on connect — a WebSocket URL shows up in proxy logs, a session token must not.

| socket | what for |
|---|---|
| `/ws/shell` | the terminal (raw bytes in both directions) |
| `/ws/whatsapp` | messages arriving live |
| `/ws/videocall` | WebRTC signalling |
| `/ws/mobile-events` | deploys, alerts, queue — **a single** socket, with per-open-screen subscription, which is never left open in the background |

### 7.5 When there is no network

The industry standard for this is *offline-first* with local data as the source
of truth (Room) plus `WorkManager` — that is what Google's official guide and
*Now in Android* describe. What is implemented is the **cheap, immediate half**
of that pattern: an HTTP cache for reads, a queue for writes. It is stated here
so it is not read as more than it is.

**Reads — the cache.** `CacheDeLeitura` installs a 24 MiB disk cache in OkHttp
and covers **all 72 routes at once**, because it acts on the HTTP client and not
screen by screen. There are two interceptors, and the split matters:

| piece | type | what it does |
|---|---|---|
| `TornaCacheavel` | **network** interceptor | rewrites the response's `Cache-Control` so it can be stored; honours `no-store`; stamps the time |
| `ServeDoCacheQuandoAFalta` | **application** interceptor | on `IOException`, repeats the request with `onlyIfCached` + `maxStale` |

Rewriting in the network interceptor is not a detail: by the application
interceptor it is already too late, the response no longer passes through the
cache. With a network, freshness is **zero** — the cache never wins over a fresh
response. Without a network, seven days. The cache is wiped on *sign-out*.

**The label.** On its own, the cache creates a worse problem than it solves: the
screen starts claiming, with the same face as always, that the disk is at 78% —
when that number may be six hours old and the disk may already be full. **Stale
data with no label is worse than an error screen**, because an error screen
never stops anyone from acting. `FaixaDeOffline` appears only when there is no
**validated** network (`NET_CAPABILITY_VALIDATED`, which distinguishes "there is
an interface" from "the internet works"), and it says how long ago the last
server response was. The stamp comes from the network interceptor — the only
point in the app that knows, for certain, that the response did not come from
the cache. The banner says *"last server response"* and not *"this data is
from"*: the stamp is for the whole app, and the second phrasing would be more
precise than what is actually known.

**Writes — the queue.** A cache serves a stored response; a write has no
response to store, and serving a `POST` from cache would be inventing that
something happened. They are different mechanisms by nature. `FilaDeEnvio` is
an *outbox*: the action is written to disk and drained by a `CoroutineWorker`
with a network constraint — `WorkManager` because the action has to survive the
app closing and the device rebooting. FIFO with unique chained work, because
two actions on the same resource out of order produce a state nobody asked for.
`4xx` is not retried: the server understood and refused, and insisting only
burns battery.

The condition for entering the queue is the idempotency proof, for the reason
established in §5.8.

## 8. Identity, session and security

### 8.1 Signing in

1. **Passkey** (WebAuthn) through the Credential Manager, against
   `go-webauthn`. This is the default on an already registered device.
2. **QR pairing** — the desktop panel shows the code, the app reads it with the
   camera and receives the session. Scanning the QR became the **default
   first-use flow**: the QR also carries the server address, so a new device
   gets in without typing anything.
3. **Username and password + TOTP**, reusing the desktop MFA policy.

Passkey registration **never issues a token** — there is a test pinning that —
and resistance to user enumeration holds on every path.

### 8.2 Where the session lives

- `TokenStore` — `EncryptedSharedPreferences` (AES256-GCM, key in the
  Keystore), falling back to plain preferences if the Keystore fails on first
  boot (otherwise the app does not open).
- `AppSession` — the live state of the process, **exactly one**, observed by the
  network interceptor and by `MainActivity`. That is why a `401` on any screen
  returns to login with no explicit error navigation: what swaps the screen tree
  is the `when` that observes `session.state`.
- `AuthTokenInterceptor` injects the token; `BffSessionRefresher` renews through
  `/auth/refresh` with a **rotating refresh token**, in a single queue (one
  refresh at a time, *fail-closed* guard).
- `ServerConfigStore` holds the server address, also encrypted — the address is
  never less protected than the credentials that will be sent to it.
- Signing out drops **only that device's session**, and the desktop panel lists
  and revokes paired devices.

### 8.3 RBAC

Filtered **on the server, by omission**: what the user may not see is not sent.
Never "sent and hidden in the client". `GET /screens/{id}` answers **404, not
403**, so it does not leak the existence of a section.

### 8.4 App identity confirmation

`/.well-known/assetlinks.json` publishes the SHA-256 fingerprints of the
accepted keys. It is a **list**: during the transition from the development key
to the release key, devices signed with either one keep working with passkeys.

---

# Part III — What it does

## 9. The screens, one by one

Navigation is a **drawer**, not a bottom bar: there are eight destinations, and
the Material 3 `NavigationBar` is specified for three to five — with eight, each
label would wrap onto two lines and still be clipped. There is a test that reads
the `TextLayoutResult` of every label and fails if any of them exceeds one line,
so a name that is too long breaks the build instead of arriving crooked on the
device.

| group | destinations |
|---|---|
| **Operations** | Terminal · Admin · Files |
| **Communication** | Call · WhatsApp · Notifications |
| **Session** | Home |
| **About** | Licenses |

The footer holds what belongs to the **device**, not to the server: appearance
(light/dark/system), **Check for updates** with the installed version, and
**Sign out**.

### 9.1 Home — the operations panel

This is not a welcome screen. It brings together, in one view: machine
resources (CPU, memory, disk, network, uptime, via `/ops/status`), identity and
server, recent and scheduled deploys. The thresholds are **honest** — swap with
no headroom and stolen CPU become signals, instead of everything being green.
Each card navigates to the matching screen.

### 9.2 Terminal — the reason it exists

The largest module (63 sources), with its own section further down (§10). In
short: a session list (`dtach` on the server, so they survive closing the app),
a grid drawn on `Canvas`, a three-state extra-keys bar, paste, per-cell
selection, attachments, session backup/restore, and the appearance preferences.

### 9.3 Admin — Server-Driven UI

**Administration opens on a launcher, not on a screen.** This was a reach
defect, not an aesthetic one: the drawer's "Admin" destination opened a
hard-coded `scheduler.jobs`, and the other **24 screens the server was already
serving were unreachable from the phone** — the whole SDUI existed and nobody
got to it. Today the route with no named section resolves to **null**, and null
is the launcher:

| piece | what it is |
|---|---|
| launcher | adaptive card grid (minimum 112 dp, height 128 dp), with a search field and recent sections at the top |
| command palette | bottom sheet with automatic focus (toolbar button or `Ctrl+K`), to reach any section without going back to the launcher |
| search | `filtrarSecoes`, **a single** implementation used by both — two would disagree about what a term finds |
| recents | last six sections, by id, in `SharedPreferences` |

A deep link still beats everything: a notification pointing at
`docker.containers` opens right there, because the question "which section" was
already answered by whoever sent the notification. Closing the section returns
to the launcher **and clears the choice** — without clearing it, the next reload
would reopen the section the person just closed. The seven points are pinned by
an invariant in the deploy gate.

The card height is 128 dp and not 108 because three real labels wrap onto two
lines (`Imagens do Docker`, `Serviços (systemd)`, `Aparelhos (VLESS)`); and the
group name is still spelled out in full because the initial does not
disambiguate — `Sistema` and `Segurança` are both `S`.

`GET /screens` lists what the server offers — **25 reachable screens** — and
`GET /screens/{id}` returns the screen as a component tree that `:sdui` draws in
Compose:

| component | what it is |
|---|---|
| `form` | form with validation declared by the server |
| `table` | table with typed columns |
| `list` | list with per-item actions |
| `detail` | record view of an object |
| `chart` | time-series chart |
| `action` | button that fires `POST /actions/{id}` |
| `confirm-destructive` | mandatory confirmation before anything irreversible |

The vocabulary is **closed and versioned**, with guards against nesting and
against conditionals — an unknown component is skipped safely, and one marked as
critical shows "update required" instead of taking down the screen. Validation
errors come back **keyed by field** and stick to the field that caused them.
Destructive confirmation is enforced on the **server**, not only in the client.

The tooling shipped together with the pilot screen, not after it:
reflection-based contract generator, a corpus of golden fixtures, frozen client
manifests, a breaking-change detector and a CI gate (`make sdui-check`) that
rejects a server change capable of breaking an already published app.

### 9.4 Files

Remote browser, read and write with **mtime-based conflict detection** (409 with
on-screen resolution), sora-editor with TextMate highlighting for the backend's
7 languages, and a resumable transfer engine on top of `androidx.work`:

- **Download:** the resume *offset* is derived from the real size of the
  MediaStore entry — self-correcting by construction, with no counter to fall
  out of sync.
- **Upload:** `init` → `chunk` → `complete`, with resume state in
  `TransferStateStore` (`SharedPreferences`), because WorkManager's `Data` is
  fixed at enqueue time and cannot change during an attempt.
- **Share target:** the app appears in Android's "Share" menu, with destination
  selection and name-collision handling when several items are shared at once.

### 9.5 WhatsApp

Chat list with unread markers, conversation with paginated history,
**idempotent** text sending, media sending with progress and retry, read
receipts, avatars. New messages arrive over `/ws/whatsapp`, not by polling.
Images and video render inline; audio has a shared player with a progress bar;
documents open through `FileProvider`. The media cache is size-bounded.

### 9.6 Video call

P2P mesh with ephemeral TURN — the server only does signalling, it does not
carry media. On the device: `stream-webrtc-android`, a *foreground service* of
types `camera`/`microphone`/`phoneCall` **for the duration of the call**, a
self-managed `ConnectionService` with `CallStyle` through Telecom so the call
shows up as a real call, and `USE_FULL_SCREEN_INTENT` to ring on the lock
screen. A Telecom registration failure **must not leave the ringer silent** —
there is a fix and a test for that — and the Telecom account check is done
without requiring a dangerous telephony permission.

### 9.7 Notifications

A `push` channel in the server's `internal/notify` (the fifth channel, next to
inapp/webhook/telegram/email/whatsapp), delivered by FCM, reusing the same
rule/throttle/dedup engine as the others. The preferences screen picks what
arrives **per rule**, not through an all-or-nothing switch. Tapping the
notification opens straight onto the relevant screen, already loaded.

### 9.8 Licenses

The LGPL-2.1 attribution obligation from sora-editor, plus the vendored native
code (libghostty-vt, HDiffPatch). It has to be reachable; it does not have to
compete for space with the Terminal — hence the "About" group, separated by a
divider.

### 9.9 Diagnostics

The `diagnostico` route, with no drawer entry: it is the destination of the
"Diagnostics" button on the update banner. It shows initialization failures, the
previous process *crash* and the history of update failures — full text,
selectable.

It exists for a concrete reason: the app was built with no device at hand and
the owner works from the phone only, with no `adb` and no logcat. When something
breaks, this screen is the only channel through which information leaves the
device.

## 10. Inside the terminal

### 10.1 The emulator

`libghostty-vt`, from the Ghostty project (MIT, Zig), compiled for
`arm64-v8a`/`x86_64` and exposed through JNI (`ghostty_jni.cpp`). The commit is
**pinned exactly** (`b09473…`) because the C ABI is declared unstable by the
project itself; the Zig version is read from the `build.zig.zon` of **that
commit**, not of HEAD.

The division of labour is the standard one for any terminal with a native
engine:

- **The engine owns** escape *parsing* and grid state.
- **The host owns** drawing and input.
- At the boundary, a *snapshot* copied under a lock — never a live reference.
  (The first cut leaked an ART global reference and swapped the buffer under the
  snapshot; both were fixed on day 2.)

Kotlin-side API: `write`, `snapshot`, `resize`, `modes`, `encodeMouse`,
`encodePaste`, `scrollViewport`, `scrollToBottom`, `scrollState`, `close`.

**Synchronization:** every mutation is funnelled through a single dispatcher
(`Dispatchers.Main.immediate`), even though the bytes arrive on the WebSocket
thread. The native guarantee covers concurrent `write`/`snapshot`, but not
`resize` against either — the funnel eliminates the race instead of relying on
the narrower guarantee.

### 10.2 Drawing

A custom rasterizer (`FrameRasterizer`, `RowRasterizer`, `GlyphAtlas`,
`GlyphSlotAllocator`, `TerminalCanvas`). The atlas has a **fixed** memory cost,
independent of history size or of how many distinct glyphs the session uses.
Cell metrics are rounded to whole pixels — the condition for a 1:1 *blit*. An
alternative `SurfaceView` was implemented and **measured** against `Canvas`;
`Canvas` won.

Two rendering defects already paid for: the atlas rasterized in Roboto instead
of the monospaced face (text came out with gaps), and `Canvas` did not clip (the
grid painted over the rest of the screen).

### 10.3 Input

- `TerminalInputView` + `TerminalInputConnection`: a `View` that exists to
  receive the IME and convert it into **bytes**, not into text. Never an
  `EditText` and never a `BasicTextField` — dual ownership of text is what
  brought down the earlier attempt. The terminal's content selection **shares no
  state** with the IME composition.
- `ExtraKeysBar`: `Esc`, `Ctrl`, `^C`, `Tab`, arrows — a three-state bar, with
  the occasional controls out of the main column.
- `PendingModifiers`: sticky modifiers (tap `Ctrl`, then the letter).
- `HardwareKeyHandler`: physical Bluetooth/USB keyboard, including Ctrl/Alt.
- `RoteamentoDeToque` / `MouseReportGestureController`: when the remote program
  **turns on mouse reporting** (DECSET 1000/1002/1006), a touch becomes a mouse
  event; otherwise, local scrolling. What decides is the real mode reported by
  the engine, never a guess.
- **Paste:** the app brackets it (DECSET 2004) **if and only if** the remote
  program turned the mode on. Previously the server bracketed unconditionally —
  and the server emulates no terminal at all, so it had no way to know. The
  result was a literal marker on the command line, or a multi-line text executed
  on the spot. The paste goes out as already encoded bytes, in a single binary
  frame, preserving atomicity.

### 10.4 Geometry and the soft keyboard

See §5.1–5.3 for the story. The current state:

1. **220 ms debounce** — only the size at which the grid *settles* leaves here.
   The value covers Android's IME animation (~200 ms; the Material spec calls
   for 250 ms on large transitions) without noticeably delaying a screen
   rotation.
2. **The grid does not shrink with the keyboard.** `GeometriaDaGrade` is a
   stateless `object`: `alturaSemTeclado = height + max(0, ime − navigationBar)`,
   recomputed every frame. The exact subtraction comes from the `AppNavHost`
   inset chain (`padding` → `consumeWindowInsets` → `imePadding`), which makes
   the correction exact rather than heuristic.
3. **Anchored to the CURSOR**, with two lines of slack
   (`LINHAS_ABAIXO_DO_CURSOR = 2`). The grid is measured at full height and
   *shifted* in the placement phase — reading state in `layout` repositions
   without re-measuring, and pointer coordinates follow the placement, so hit
   testing needs no correction.

**`LinhasVisiveis`** inverts the question: instead of picking the font size and
counting how many lines fit, you pick the number of lines (auto/24/30/36/45/60)
and the app **derives** the size by search — largest to smallest, returning the
first one that actually fits. It is a search and not a division because cell
height is not linear in font size (whole-pixel rounding plus line spacing as an
integer delta), and one extra pixel per line, over 45 lines, is a whole line
lost.

### 10.5 Conversation history

`dtach` does not keep the screen. On attach, the grid is born empty and the
previous history lives in the server's **pty log**
(`data/users/<user>/session-logs/<session>.log`, rotated at 8 MiB with one
previous generation — a 16 MiB ceiling). **The log only records while someone is
attached.**

```
GET /api/mobile/v1/terminal/log-bruto?name=<session>&bytes=<cap>
      ↓  raw PTY bytes, in base64
  TerminalRepository.logBruto()     (decodes off the main thread)
      ↓
  TerminalViewModel.iniciarPrimer()
      ↓  writes into libghostty-vt in 256 KiB chunks, yielding
  scrollback ready  →  releases the live stream that was held back
```

Details that hold this up:

- **base64, not a JSON string.** The log is not text: it has control bytes and,
  after a rotation in the middle of a character, truncated UTF-8 sequences. A
  JSON string field would escape every ESC into six characters (`\u001b`) and
  replace every invalid byte with U+FFFD — corrupting exactly the sequences that
  give the stream its meaning.
- **The cut starts after a line break.** Cutting by byte can land in the middle
  of an escape, and half a sequence is garbage printed on screen.
- **Order.** Live bytes arriving during the fetch sit in a queue (capped at
  2 MiB) and only enter the engine after the history. If the cap is exceeded the
  history is abandoned — losing history is annoying, freezing the live terminal
  is a defect.
- **No sanitizing the end of the replay.** If the session is currently in `vim`,
  the log ends inside the *alt-screen* and that is the engine's correct state. A
  faithful replay leaves the engine exactly where a terminal that had watched
  the whole session would be — including the modes the gesture layer queries.
- **`replay=0`, not `attach=1`.** They are different questions: `attach=1` says
  "I already have the screen" (reconnection) and also disables the
  *repaint-wobble*; `replay=0` waives only the history block. The wobble is
  still essential, because what the program did while nobody was attached is in
  no log at all.
- **How many lines.** The ladder is 5/10/20/50 thousand and it drives both
  halves: the emulator's capacity and the byte cap fetched. The cap comes from
  the **worst case measured** in the real logs on this machine — 1,300 B per
  rendered line:

  | log | profile | bytes per rendered line |
  |---|---|---:|
  | `main.log` | shell | 244 B |
  | `Servidor.log` | mixed | 400 B |
  | `Vpsm.log` | mixed | 667 B |
  | `Aplicativo.log` | Claude Code | 1,250 B |

  A five-fold spread. An average ratio would leave precisely the conversation
  session — the one you want to reread — half as long as it should be. On the
  wire, transport gzip shrinks the log ~15× (measured: 6.2 MB → 388 KB), and the
  fetch happens once, on attach.

### 10.6 Reconnection and the background

Measured: Android 15+ cuts the app's network ~5.7 s after it leaves the
foreground (`FIREWALL_CHAIN_BACKGROUND` firewall chain) and destroys the
sockets; blocked, the reconnection loop never even emits a SYN. That is why
returning to the app **reconnects immediately** instead of waiting for the
backoff — and that reconnection carries `attach=1`, so it does not redraw over
the grid already in memory.

The "Reconnecting…" banner has a 1.5 s grace period: a healthy reconnection
takes 0.5 to 1 s (timed with `tcpdump` on the emulator), so an everyday app
switch lights up no banner at all. An attempt that did come alive **resets** the
backoff ladder — without that, five trips to the background left the return
stuck for 15 s on "Reconnecting (6)…" with the server up.

**No *foreground service* to keep the session alive.** The session lives on the
server, in `dtach`. The app is free to die.

---

# Part IV — Life cycle

## 11. Updating the app itself

### 11.1 Why it is not just "download the APK"

The owner's internet is poor and the APK is tens of megabytes. The app uses an
**incremental channel** with HDiffPatch: it downloads a `.hdiff` from the
installed version to the new one and rebuilds the APK on the device.

Keying is by the **SHA-256 of the installed APK**, never by `versionCode`: a
binary patch is a function of the base's exact bytes, and two builds of the same
`versionCode` (rebuild, re-signing, different ABI) have different bytes.
Applying the wrong patch does not produce a version error — it produces a
corrupt file.

The "full" artifact **is also a `.hdiff`** (`hdiffz` with an empty base). That
keeps ONE code path on the device: what changes is the base file, not the
algorithm.

The byte route uses `http.ServeContent`, which gives you correct
`Range`/`If-Range`/`If-None-Match`/206 for free — that is, **real resumption**.
Writing it by hand (`Content-Length` + `io.Copy`) is precisely what does not
resume.

The channel is **authenticated**, and not by encryption: a binary patch is a map
of which parts of the code changed between two versions, and the repository is
private. Publishing it openly would hand a source diff to people with no access
to the source.

### 11.2 The inviolable rule

An APK whose SHA-256 has not been verified **never** reaches the installer.
`hpatchz` applied over the wrong base returns SUCCESS and writes a complete,
wrong file — its return code is evidence of nothing. The one that verifies is
`:patch-engine`, and `PatchResult.Applied` is the only accepted outcome.

### 11.3 The fallback ladder

| situation | what happens |
|---|---|
| unknown base (dev build, version outside the window) | the server sends `patch: null`; it goes through the full artifact |
| the `.hdiff` arrived corrupt | download it again ONCE; after that, step down a rung |
| rebuilt APK with a different hash | discard it, fall back to the full artifact, record the base hash in Diagnostics |
| not enough space | it says how many MB are missing and does not start |
| download interrupted | the partial stays on disk; the next attempt resumes through `Range` |
| installation refused by the session | opens the **system installer** with the already verified APK |
| "install unknown apps" turned off | direct shortcut to the switch |
| device blocks sideloading | says so plainly and points to `/android/install` |
| unexpected exception | safety net: records it in Diagnostics and allows a retry, instead of hanging on "Installing…" forever |

A dropped connection does **not** step down a rung: the full artifact is several
times larger and would drop just the same; what fixes it is resuming the
partial. Corrupt bytes twice in a row, yes.

### 11.4 Checking for updates: automatic and manual

**Automatic** — `UpdateCheckWorker` (WorkManager, periodic) plus one check on
every launch with a valid session. It is **silent on purpose**: a network error
and "nothing new" do not raise a banner. A warning that shows up all the time
and is not actionable is noise you learn to ignore, including when it finally
does say something. When it finds a new version, it raises the banner with the
**size of what will be transferred** — with this internet connection, that
number is the most important information on the screen, and it is always the
transfer size, never the size of the rebuilt APK.

**Manual** — "Check for updates", in the drawer footer, with the installed
version right below it (the one piece of information that makes the answer
verifiable). All three points invert, because an explicit question was asked:

- failure is reported;
- "nothing new" is reported;
- finding a new version **starts the update right away**, without demanding a
  second tap on a banner that appears behind the drawer.

Both answers dismiss themselves after 6 s and carry a "Close". Android's
confirmation stays exactly where it always was: nothing is installed without the
system dialog. What the button removes is the wait, not the consent.

## 12. Build, signing and distribution

### 12.1 Key custody — the rule that is not negotiable

The **release** signing key is generated and used exclusively on the operator's
offline machine. It **never** exists on this VPS or in the lab, because it has
to survive a compromise of this server — which is public. For the same reason,
the `repokey` that signs the F-Droid index is offline too, and `fdroid update`
never runs here.

For working builds there is a **development** key, disposable by construction
and not by agreement: a file with a different name
(`vpsmanager-DEV-NAO-E-RELEASE.jks`), a different alias, a CN that says
"DEV - NAO E RELEASE", a shorter validity, and a deliberately public password
(like the `android` of the SDK's own debug keystore — keeping the password of a
disposable key in the vault would only teach people to treat the vault as a
dumping ground).

The release build comes out **unsigned** without the `vpsmanager.devKeystore`
property — correct and deliberate behaviour, because a `signingConfig` enabled
by default would erase the guarantee with nobody noticing. With it, it applies
the dev key and marks the `versionName` with `-devsigned`.

There is a keystore **restore drill**, tested, held to the same standard as
`secrets.vault`.

### 12.2 Publishing

| script | when |
|---|---|
| `scripts/android-publish.sh <versionCode>` | a real release: requires the offline-signed APK and the F-Droid index already regenerated with the repokey. Checks the fingerprint with `apksigner` before publishing. |
| `scripts/android-publicar-devsigned.sh <versionName> <versionCode> [apk]` | a working build signed with the dev key, **from here**. Reads the real `versionName` from the APK with `aapt2` and refuses to publish if the `versionCode` does not match the one requested. |
| `scripts/android-patches.sh` | generates the incremental channel. Called by both of the above. |

**Publishing always happens in both places** — the F-Droid repository *and* the
incremental channel. See §5.6 for what happens when it does not.

Two operational notes about the repository: the F-Droid client **does not follow
3xx redirects**, so the APK is served straight from the path the index
references (`http.ServeContent`, never `http.FileServer`); and the
`/android/install` page is authenticated, inside the panel itself, with a
scannable QR — no external site.

### 12.3 Android developer verification

Google's requirement reaches Brazil on 2026-09-30. The locked way out is the
limited-distribution account (20 devices, free, no identity document), which
covers exactly this case. See §3 for the contradiction in Google's documentation
and why the date was kept as an internal target.

## 13. Tests

171 test sources for 260 production sources. The strategy, by layer:

| layer | how it is tested |
|---|---|
| pure logic (geometry, update decision, gesture policy, SDUI version tolerance) | plain JUnit, no Android |
| ViewModels | `kotlinx-coroutines-test` with a virtual dispatcher and doubles at the boundaries |
| Compose | Robolectric — **every** screen of the app is rendered in every state |
| drawer labels | measured `TextLayoutResult`: a label that wraps onto two lines fails the build |
| terminal rendering | corpus of 11 VT fixtures with *cell-level* and *pixel-level* conformance against PNG goldens |
| network | doubles of the `:data` interfaces; no test touches a real socket |
| JNI | instrumented tests + a soak with `CheckJNI` (no monotonic growth of the native heap) |
| SDUI contract | per-role goldens on both sides + `make sdui-check` |
| performance | `:benchmark` (Macrobenchmark) and baseline profiles |

Boundaries such as `ApkInstallerPort` and `ApkPatcherPort` exist because
`PackageInstaller` and `hpatchz` do not exist on a host JVM — that is what makes
it possible to pin every rung of the update ladder in a plain test.

**What the tests do not cover, and real hardware does:** there is a KVM-capable
Android emulator on a lab machine. No Android binary ships without being run
there. Traps already paid for on that path, all of them recorded:

- `input keyevent 4` with the keyboard closed **navigates back** instead of
  closing the IME — a "test" turns into a photo of the Android home screen;
- sending EOF over a `dtach` socket **kills the session's shell**;
- the emulator's IME **capitalizes the first word** typed;
- scripts with fixed coordinates break when a new banner (the update one, for
  example) pushes the layout down.

## 14. Version history

The channel keeps a window of 5 versions. Milestones:

| version | what changed |
|---|---|
| 0.1.12 | first attempt against the duplication (history clear on attach) — created a worse defect |
| **0.1.17** | **broke the screen**: geometry with memory, reverted right after |
| 0.1.18 | emergency revert |
| 0.1.19–0.1.20 | stateless geometry, cursor anchoring |
| 0.1.21–0.1.22 | sessions page with density, actions and backup |
| **0.1.23** | auto-update unblocked ("Self update is blocked…"), keyboard stops clipping the input box, visible-lines control |
| **0.1.24** | conversation history replayed from the raw bytes; `replay=0` separated from `attach=1` |
| **0.1.25** | "Check for updates" button; publishing to both channels through the repository script |
| **0.1.26** | Administration opens on a launcher (search, palette, recents); the client re-asserts the grid size on every connection |
| **0.1.27** | offline layer: read cache across all 72 routes, stale-data banner and write queue |

## 15. Locked decisions

Not to be re-derived without a new reason:

1. **No browser engine.** No TWA, WebView, Capacitor or Tauri.
2. **`minSdk` 34.** The fleet is 100% Android 14+.
3. **The BFF as the single contract**, with OpenAPI generating the client. It is
   what prevents the divergence that killed `/m/`.
4. **SDUI for the long tail.** A new admin screen is not an app release.
5. **libghostty-vt with a pinned commit.** The C ABI is unstable by the
   project's own declaration.
6. **The release key never touches this VPS.**
7. **Keeping the session alive is the server's job** (`dtach`), not a
   *foreground service*'s.
8. **RBAC by omission, on the server.** Never sent-and-hidden.
9. **Out of scope by design:** the tabs that are literally a browser, and
   game-server sections.
10. **The cache never serves without a label.** Cached data without the banner
    is worse than no cache at all: it states an old number with a current face.
11. **The write queue requires a declared idempotency proof**, with no default
    value. Opening the queue to other routes is a server change.
12. **Administration opens on the launcher**, never on an arbitrary section.

## 16. Where to read next

| document | subject |
|---|---|
| `docs/android-incremental-updates.md` | the patch channel in detail, with the measurements |
| `docs/android-signing-keystore.md` | custody of the release key |
| `docs/android-dev-key.md` | the disposable key and its limits |
| `docs/android-fdroid-repo.md` | the repository served by this host |
| `docs/android-release-pipeline.md` | the operator's full flow |
| `docs/android-unifiedpush.md` | push alternative without Google |
| `docs/android-developer-verification.md` | Google's requirement and the way out |
| `docs/android-ux-research.md` | the market research behind the screens |
