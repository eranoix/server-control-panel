# Android app UX/UI research — what to do, in this order

Written on 2026-09-06, for the native app (Kotlin + Compose, Material 3) of
vps-manager. **This document is opinionated on purpose.** Where there is a
choice, it makes the choice and says why; every source is cited with a URL, so
it can be checked. Where a claim is my judgement and not the source's, it is
marked **(opinion)**.

The motto is *"a terminal in your pocket: run the server from anywhere with
desktop quality"*. Everything below is measured against that.

---

## 0. The seven decisions, on one page

| # | Decision | Where | Effort |
|---|---|---|---|
| 1 | The extra-keys bar becomes **three-state chrome** (collapsed / 1 row / 2 rows), with **swipe-up** on each key giving it a second function, and **auto-collapse when a physical keyboard is present** | Terminal | Medium |
| 2 | The rest of the terminal chrome (mouse mode, font size, scrollback) **leaves the column** — today those are 3 permanent rows eating ~150 dp | Terminal | Small |
| 3 | **Home becomes an operations dashboard**, ordered by severity: *what is broken → what is running now → what I am about to do → who I am* | Home | Large |
| 4 | **Admin gets a section picker.** The server already exposes **25 SDUI screens**; the app routes to **one**. This is the single largest cause of "empty pages" | Admin | Small |
| 5 | Every empty state must **teach and offer an action**, never just report emptiness | All | Small |
| 6 | The drawer gives up its work destinations to a **bottom bar of 3–5 items** (the Android docs advise against a drawer on compact screens, on ergonomic grounds) | Shell | Medium |
| 7 | **One Glance widget and one Quick Settings tile**: health and queue without opening the app | Outside the app | Medium |

Execution order, with effort item by item: §10.

---

## 1. The diagnosis: why the pages look empty

Before the research, a survey of the code itself — because two of the owner's
three complaints have a mechanical cause, not an aesthetic one. Fixing the cause
is cheaper than redesigning.

### 1.1 The content already exists on the server; the app never asks for it

`internal/mobilebff/screens/` defines **25 complete SDUI screens**, with tables,
actions and destructive confirmation:

```
alerts.rules       docker.compose     docker.containers  docker.images
docker.networks    docker.prune       docker.volumes     ai.settings
deploy.apps        jira.issues        queue.jobs         scheduler.jobs
security.adguard   security.audit     security.devices   security.economia
security.secrets   security.sessions  security.ufw       security.users
system.history     system.metrics     system.ports       system.processes
system.systemd
```

The app has a generic host able to render all of them — `AdminScreen.kt` "never
inspects which components a screen contains". But `AppDestination.kt` pins the
target:

```kotlin
val navigationTarget: String
    get() = if (this == Admin) adminSectionRoute(SCHEDULER_SECTION_ID) else route
```

**The drawer's "Admin" item opens `scheduler.jobs` and nothing else.** The other
24 screens — Docker, processes, ports, systemd, UFW, AdGuard, audit, and
`system.metrics` with **three CPU/memory/disk time-series charts** — are live,
behind RBAC, and unreachable from the phone. A comment in the code already flags
this as "the first thing to remove".

Worth correcting a common assumption about this app: **there is indeed a CPU/memory/disk
endpoint in the BFF** — `GET /api/mobile/v1/system/metrics/{cpu,mem,disk}`,
declared in `mobile-v1.yaml` and wired to the three `ChartComponent`s of
`system.metrics`. What is missing is not the data, it is the **aggregated
instantaneous value** (the endpoint returns a series for a chart, not "CPU right
now = 34%"). See §3.4.

**This is most of the "the other pages have to have real content" problem** —
and the fix is a section picker, not new content.

### 1.2 Home throws away 3 of the 5 fields it already receives

`GET /api/mobile/v1/me` returns `user`, `email`, `is_admin`, `capabilities[]`
(`admin.full`, `docker`, `docker.write`, `users`, `users.manage`) and
`server_time`. `HomeScreen.kt` renders `user` and `email` in a centered `Card`
and discards the rest. It is an identity screen in an app whose owner asked for
a dashboard.

### 1.3 The terminal stacks four pieces of chrome before the first shell pixel

`TerminalRoute.kt`, in the order it composes the `Column`:

```
TopAppBar                (title + back)              ~64 dp
ConnectionBanner         (connection state)          ~40 dp
MouseReportingToggleRow  ("Mode: text selection")    ~48 dp  ← always visible
FontSizeControlRow       ("Font size: 14sp")         ~48 dp  ← always visible
ScrollbackPanel          ("Load scrollback…")        ~48 dp  ← always visible
──────────────────────────────────────────────────────────
                                              ~248 dp
[ terminal grid ]
"Hide keys ▾"            (a row for the button alone) ~48 dp
ExtraKeysRow             (the key row)               ~48 dp
```

On a screen of ~800 usable dp that is **~345 dp of chrome, ~43%** — before the
soft keyboard opens. With the keyboard (~250–300 dp), the grid has room for
**5 to 7 lines of text**. That is why "the terminal menu is too big": it is not
the key bar alone, it is the sum. **(opinion)**

---

## 2. Terminal — the extra-keys bar

Priority 1, together with §3. It is the screen the app exists for.

### 2.1 What the others do, with numbers

These apps do not publish heights in their docs, so I went to the source.

#### Termux — the most informative, because it is auditable

- The bar is **37.5 dp per row**, fixed in the layout, multiplied by the number
  of configured rows and by a user scale factor
  ([`activity_termux.xml`](https://github.com/termux/termux-app/blob/master/app/src/main/res/layout/activity_termux.xml),
  [`TermuxActivity.setTerminalToolbarHeight()`](https://github.com/termux/termux-app/blob/master/app/src/main/java/com/termux/app/TermuxActivity.java)).
  Note: **37.5 dp, not 48** — Termux knowingly breaks the Material minimum touch
  target to buy grid height.
- The factory default is **two rows of seven keys**
  ([`TermuxPropertyConstants.DEFAULT_IVALUE_EXTRA_KEYS`](https://github.com/termux/termux-app/blob/master/termux-shared/src/main/java/com/termux/shared/termux/settings/properties/TermuxPropertyConstants.java)):

  ```
  [['ESC','/',{key:'-',popup:'|'},'HOME','UP','END','PGUP'],
   ['TAB','CTRL','ALT','LEFT','DOWN','RIGHT','PGDN']]
  ```

  The single-row alternative, commented out in the same file, also has **seven
  keys**: `[[ESC, TAB, CTRL, ALT, {key:'-',popup:'|'}, DOWN, UP]]`.
  **Seven per row is the empirical answer to "how many fit".**
- Each key can carry a **`popup`**: a second function fired by a **swipe-up**
  over the button. It doubles capacity **without spending a pixel of height**
  ([`ExtraKeysInfo.java`](https://github.com/termux/termux-app/blob/master/termux-shared/src/main/java/com/termux/shared/termux/extrakeys/ExtraKeysInfo.java)).
- There are **macros**: `{macro: "CTRL f d", display: "tmux exit"}` — one key
  that fires a whole sequence. The advanced example in that file's own header is
  literally a tmux keyboard (same source).
- Arrows and backspace **auto-repeat on long-press**, with
  `DEFAULT_LONG_PRESS_REPEAT_DELAY = 80` ms
  ([`ExtraKeysView.java`](https://github.com/termux/termux-app/blob/master/termux-shared/src/main/java/com/termux/shared/termux/extrakeys/ExtraKeysView.java)).
- The bar is a **two-page ViewPager**: page 0 = extra keys, page 1 = **a text
  field** to type and send a whole line
  ([`TerminalToolbarViewPager.java`](https://github.com/termux/termux-app/blob/master/app/src/main/java/com/termux/app/terminal/io/TerminalToolbarViewPager.java)).
  A horizontal swipe switches between them.
- Toggling the bar: `toggleTerminalToolbar()`, bound to **Volume-Up + Q/K**
  ([`TermuxTerminalViewClient.java`](https://github.com/termux/termux-app/blob/master/app/src/main/java/com/termux/app/terminal/TermuxTerminalViewClient.java)).
- Ctrl/Alt are **sticky** (they hold until the next key); arrows are one-shot.
  Volume-Down acts as **Ctrl** when there is neither a bar nor a physical
  keyboard ([issue #97](https://github.com/termux/termux-packages/issues/97)).

**Termux's complaint #1 is exactly our risk:** the bar keeps taking space even
with a Bluetooth keyboard attached, and hiding it requires a manual long-press —
a recurring request, unresolved for years (issues
[#556](https://github.com/termux/termux-app/issues/556),
[#3121](https://github.com/termux/termux-app/issues/3121)). Worse: users report
that `extra-keys-style = none` and `extra-keys = []` **do not hide** the bar.

#### The other seven

- **Blink Shell (iOS)** does the opposite, and gets it right: the "Smart Keys"
  **exist only while the soft keyboard is open — they disappear on their own
  with an external keyboard** ([docs.blink.sh](https://docs.blink.sh/)). Holding
  a modifier lets you chain combinations (C-x C-c). Modifiers are remappable per
  side, including Caps as Esc-alone / Ctrl-combined
  ([customize.md](https://github.com/blinksh/docs/blob/master/src/pages/basics/customize.md)).
  Gestures: two fingers = new shell, horizontal swipe = switch shell, pinch =
  font size.
- **Prompt (Panic)**: **16 customizable keys on iPhone, 32 on iPad**, behind an
  "Extra Keyboard Row" toggle. The catalogue includes F1–F16, Page Up/Down,
  Home, End, Clear Screen, Hide Keyboard, Toggle Tabs
  ([help.panic.com](https://help.panic.com/prompt/prompt-keyboard/)). There is no
  Alt: the official guidance is "Esc does the same thing as Alt".
- **Termius**: keys in **groups of 4**, reorderable; **only the first 3 groups
  (12 keys) show** above the keyboard, the rest goes into an "Extended keyboard"
  ([docs.termius.com](https://docs.termius.com/terminal/mobile-terminal)). A side
  panel carries **Snippets / Command history / Themes** tabs. Scrollback search
  **only exists on the desktop** — confirmed by support
  ([article](https://support.termius.com/hc/en-us/articles/900006226406-How-do-I-search-in-the-terminal-)).
- **Secure ShellFish (iOS)**: snippets that reproduce **any key sequence**,
  escape codes included, with delays for slow prompts; and — the detail that
  matters — a snippet can **replace the action of a bar key**
  ([help/snippets](https://secureshellfish.app/help/snippets)).
- **JuiceSSH** documents as an *official bug* that the keyboard does not hide
  itself when a Bluetooth keyboard connects
  ([FAQ](https://juicessh.com/faq/keyboard-doesnt-automatically-hide-when-bluetooth-keyboard-is-attached)).
  It uses Volume Up/Down for **font size**, not for Ctrl.
- **ConnectBot**: no permanent bar by default; tapping the screen reveals a
  **Ctrl** button; the 510 fork uses long-press on Ctrl/Esc/Sym to open dialogs
  for Ctrl+letter, F-keys and arrows
  ([510 ConnectBot](https://www.five-ten-sg.com/510Connectbot/VirtualKeyboard.html)).
  Recurring complaint: the visual cursor drifts out of sync with its real
  position.
- **a-Shell (iOS)**: no formal docs for the bar; users open *GitHub issues* to
  find out how to send Ctrl-C
  ([#168](https://github.com/holzschu/a-shell/issues/168)) and Esc
  ([#17](https://github.com/holzschu/a-shell/issues/17)). It is the
  counter-example: when the bar is not obvious, the app stops being usable for
  vim.

**A pattern across all of them, and it is a warning:** none of the eight apps has
decent client-side search in the scrollback. They all push you to
`grep`/`less`/tmux on the server.

### 2.2 The design this app should have

Today's `ExtraKeysRow.kt`: a scrollable `Row` of 9 buttons
(`Ctrl Alt Esc Tab ← ↑ ↓ → -`), with a "Hide keys ▾" `TextButton` **on a row of
its own above it**. Two rows of height to show one. Ctrl/Alt are already
three-state sticky (OFF/ARMED/LOCKED) — that is **right** and should stay; it is
more sophisticated than Termux.

Proposal: **three states, one gesture, zero wasted row.**

```
STATE A — COLLAPSED (default with a physical keyboard; and on demand)
┌──────────────────────────────────────────────────────────┐
│                                                          │
│                      terminal grid                       │
│                     (maximum height)                     │
│                                                          │
├──────────────────────────────────────────────────────────┤
│  ⌃⌥   ⌃C   ⌃D   ⇥                                  ⌃▲   │  32dp
└──────────────────────────────────────────────────────────┘
    ↑ 4 emergency keys, always present              ↑ handle

STATE B — ONE ROW (default with the soft keyboard)   ← recommended
┌──────────────────────────────────────────────────────────┐
│                      terminal grid                       │
├──────────────────────────────────────────────────────────┤
│  Esc   Tab   Ctrl   Alt    ←     ↓     ↑     →     ⌃▲   │  40dp
└──────────────────────────────────────────────────────────┘
        ↑ swipe-up on each key gives the 2nd function (table below)

STATE C — TWO ROWS (when the operator wants a real keyboard)
┌──────────────────────────────────────────────────────────┐
│                      terminal grid                       │
├──────────────────────────────────────────────────────────┤
│  Esc    /     -    Home    ↑    End   PgUp         ⌃▼   │  40dp
│  Tab   Ctrl  Alt    ←      ↓     →    PgDn    ⌘         │  40dp
└──────────────────────────────────────────────────────────┘
                                               ↑ macro drawer

SECOND PAGE (horizontal swipe over the bar, in any state)
┌──────────────────────────────────────────────────────────┐
│                      terminal grid                       │
├──────────────────────────────────────────────────────────┤
│ ┌────────────────────────────────────────────┐  ┌─────┐ │  40dp
│ │ type a command…                            │  │ ▶︎  │ │
│ └────────────────────────────────────────────┘  └─────┘ │
└──────────────────────────────────────────────────────────┘
```

**The decisions, with the reasoning:**

1. **Seven keys per row, not nine.** Nine buttons in a scrollable `Row` — what
   exists today — means the last ones **fall off screen** and the operator
   scrolls horizontally to find `→`, in an app where the arrow is the most used
   key after Enter. Seven is the number Termux converged on in both of its
   factory configurations. The `-` leaves the single row (it moves to the
   swipe-up), and `Esc/Tab` move to the front, where the thumb lands first.

2. **Swipe-up gives the second function.** It is the Termux mechanism
   (`{key: LEFT, popup: HOME}`) and the only one that doubles capacity without
   costing height. Proposed map for state B:

   | Key | Tap | Swipe-up |
   |---|---|---|
   | `Esc` | ESC | `Ctrl+C` |
   | `Tab` | TAB | `Ctrl+D` |
   | `Ctrl` | sticky Ctrl | `Ctrl+L` (clear screen) |
   | `Alt` | sticky Alt | `\|` (pipe) |
   | `←` | left | Home |
   | `↓` | down | PgDn |
   | `↑` | up | PgUp |
   | `→` | right | End |

   The arrows↔Home/End/PgUp/PgDn pairing is exactly the one in the advanced
   example in `ExtraKeysInfo.java`.

3. **40 dp of visual row, 48 dp of touch area.** Android requires a 48×48 dp
   target
   ([guide/topics/ui/accessibility/apps](https://developer.android.com/guide/topics/ui/accessibility/apps));
   Termux uses 37.5 and lives with it. Compose **automatically expands the touch
   area of a clickable smaller than 48 dp beyond its visual bounds**
   ([api-defaults](https://developer.android.com/develop/ui/compose/accessibility/api-defaults))
   — so you can have 40 dp of ink and a 48 dp target without breaking anything.
   It is the right middle ground, and the source authorizes it explicitly.

4. **The handle replaces the "Hide keys" `TextButton`.** Today a label takes a
   whole ~48 dp row **just to announce that there is a row below it**. That is
   the worst possible use of the screen's scarcest resource. The handle is a
   32 dp chevron at the right end of the bar itself: tapping cycles A→B→C→A;
   dragging vertically goes straight to a state. In A the bar collapses to
   **32 dp with four emergency keys** (`⌃⌥`, `⌃C`, `⌃D`, `⇥`) — because
   "collapsed" cannot mean "unable to send Ctrl+C", which is reason #1 for
   opening the terminal on a phone at all. **(opinion)**

5. **Physical keyboard → state A, automatically.** Blink does this by design;
   Termux and JuiceSSH have had it as an open complaint for years (JuiceSSH's is
   in the official FAQ). Detect it via `Configuration.hardKeyboardHidden ==
   HARDKEYBOARDHIDDEN_NO` and react — **automatic, but reversible through the
   handle**, never locked: Blink shows that disappearing entirely annoys people
   who want the arrows even with a keyboard attached.

6. **The bar's second page = a command field.** It is the Termux ViewPager, and
   it is the highest return per line of code on this list. A horizontal swipe
   over the bar switches "keys" ↔ "type a line and send it". It solves the real
   problem of typing `docker compose -f /opt/x/compose.yml up -d` while
   fighting autocorrect, and it **reuses height you already paid for**.
   **(opinion, on the precedent of Termux)**

7. **Long-press repeats**, 80 ms between repeats (the Termux number), on the
   arrows and on backspace. Without it, backing up 30 characters is 30 taps.

8. **Macro drawer (`⌘`, state C only).** A bottom sheet with saved sequences —
   a triple precedent: Termux's `macro:`, Termius Snippets, ShellFish snippets
   with escape codes. Start with a fixed set that is useful on *this* server:
   `Ctrl+B D` (detach tmux), `Ctrl+B [` (copy-mode), `htop`,
   `journalctl -fu <service>`, `docker ps`, `systemctl --failed`. Editable
   later. A bottom sheet and not a top menu: that is the surface the thumb can
   reach, and Material authorizes the swap when it serves a real ergonomic
   purpose
   ([breakpoints/overview](https://m3.material.io/foundations/layout/breakpoints/overview)).
   **(opinion)**

### 2.3 The rest of the terminal chrome

The three permanent rows must **leave the column**:

- **Font size** → the `TopAppBar` overflow menu (⋮), opening a bottom sheet with
  a slider. It is adjusted once per lifetime of the app; it cannot cost 48
  permanent dp. The decision recorded in the code **not to use pinch** (it would
  collide with the selection and mouse-reporting gestures on the same surface)
  is right and should stay — only where the control lives changes.
- **Mouse mode** → a **state icon in the `TopAppBar`**, not a row of text. It
  does need to be visible (it is modal; the operator has to know which mode is
  active), but visible ≠ a whole row saying "Mode: text selection".
- **Scrollback ("Load earlier scrollback")** → a gesture. Today it is a
  permanent button because `TerminalCanvas` has no scroll surface. The way out is
  to give it one: **a two-finger drag downward over the grid** loads scrollback.
  Two fingers collides with neither selection (one finger) nor mouse-reporting.
  **(opinion — none of the apps surveyed documents this exact gesture; Blink uses
  a one-finger swipe-up and admits it sometimes drags the whole window, so two
  fingers is the safer choice.)**
- **Connection banner** → it stays, but **only while not connected**. A permanent
  banner saying "connected" is noise.

That gives back ~144 dp: nearly **four more shell lines** with the keyboard open.

### 2.4 IME pitfalls that matter on this screen

`TerminalRoute` is right not to apply `imePadding()` twice — the comment in the
code describes exactly the *double padding* bug the official docs warn about
([insets-ui](https://developer.android.com/develop/ui/compose/system/insets-ui)).
Two more things worth knowing:

- `Scaffold.contentWindowInsets` **does not include the IME by default** (same
  source).
- Targeting SDK 35+, **the framework stops padding the root view automatically**;
  current guidance prefers `Modifier.fitInside(WindowInsetsRulers.Ime.current)`
  over `imePadding()`, because it reduces jank
  ([evaluate-rulers](https://developer.android.com/develop/ui/compose/system/evaluate-rulers)).
  The app targets SDK 36 — this is to be revalidated whenever the keyboard is
  touched.
- Physical keyboard: `Modifier.onKeyEvent` only fires with focus, fires on
  KeyDown **and** KeyUp, and a focused `TextField` **swallows** Tab; intercepting
  it requires `onPreviewKeyEvent`
  ([keyboard-input/commands](https://developer.android.com/develop/ui/compose/touch-input/keyboard-input/commands)).
  The app already solves this another way (a View with `setOnKeyListener` plus
  `HardwareKeyHandler`), which sidesteps the problem — worth not regressing.

### 2.5 The opportunity nobody has taken

Scrollback search. None of the eight apps has a good one: Termius admits it is
desktop-only, Blink has none, Termux tells you to use tmux. Since vps-manager
**persists the scrollback on the server** (`GET /terminal/scrollback`), this app
can offer **server-side search over the session history**, with navigable
results. It is the only feature on this list where it is possible to get
**ahead** of the state of the art instead of catching up to it. **(opinion)**

---

## 3. Home = the operations dashboard

Priority 1 alongside the terminal.

### 3.1 What the research says about the top of an operations panel

- **PagerDuty is literally "incident-first"**: the **Home** tab comes first in the
  bottom bar and "highlights your top open incidents, and shows on-call shifts
  and service information"; the order is Home → Incidents → My Shifts → Services
  → More. In the list, a **red** banner marks a triggered incident assigned to
  you and turns **yellow** once acknowledged
  ([support.pagerduty.com](https://support.pagerduty.com/main/docs/mobile-app)).
  Color is a hierarchy of urgency, not decoration.
- **Datadog** does not put the dashboard first: its official blog frames the
  problem as "the responder has no context where they are alerted", and the
  answer is **push prioritized by urgency plus lock-screen widgets** with active
  incidents, on-call, monitors and SLOs
  ([datadoghq.com/blog](https://www.datadoghq.com/blog/mobile-app-reduce-mttr/)).
- **Google SRE Workbook**: SLI metrics "should feature prominently on the
  service's dashboard, ideally on its landing page" — they are "the first
  metrics you want to check when an SLO alert fires". The dashboard should also
  show **intended changes** (binary version, flags, dynamic config version),
  dependency responses, resource saturation and traffic by status code, because
  an SLO dashboard shows *that* something is in violation, not *why*
  ([sre.google/workbook](https://sre.google/workbook/monitoring/)).
- **Inverted pyramid / aggregate status card**: PatternFly formalizes the exact
  component for "do not turn it into a sea of dots" — it shows a total and an
  **aggregate status**, with a link to the detail
  ([patternfly.org](https://www.patternfly.org/patterns/dashboard/design-guidelines/)).
- **"Worst status wins" rollup** is the industry pattern for grouping
  subsystems: "the status of the group will always be the status of the
  component with the highest severity"
  ([site24x7](https://www.site24x7.com/help/statuspage/setup-status-page/component-groups.html)).
  Atlassian's Statuspage uses groups for the same reason — they "keep your page
  clean and easy to digest"
  ([support.atlassian.com](https://support.atlassian.com/statuspage/docs/create-a-component-group/)).
- **Density**: NN/g is counterintuitive here and worth quoting — "screen space
  should not be conserved, it should be spent"; higher density is generally
  **better**, because it reduces navigation
  ([nngroup.com](https://www.nngroup.com/articles/utilize-available-screen-space/)).
  Material 3 agrees with a caveat: compact density suits "data-rich
  applications", but it **hurts the legibility of alerts**
  ([m3.material.io/foundations/layout/understanding-layout/density](https://m3.material.io/foundations/layout/understanding-layout/density)).
  Practical conclusion: **dense on data, roomy on alerts.**

A market finding worth recording: **none of the self-hosted panels has an
official app.** Portainer has had an open discussion since 2023
([#9524](https://github.com/orgs/portainer/discussions/9524)); Dokploy has an
open issue ([#3172](https://github.com/Dokploy/dokploy/issues/3172)) where the
author argues that a responsive web UI is not enough because it gives neither
native notifications nor offline caching; Linode **discontinued** its app in 2018
and recommends the responsive web
([linode.com/community](https://www.linode.com/community/questions/20748/do-we-have-any-mobile-application-for-linode));
DigitalOcean and Hetzner never had one. The state of the art of "a panel in your
pocket" is a gap — this app has no competitor to imitate, it has a vacuum to
occupy. **(opinion)**

### 3.2 The cards, in order

The rule that organizes everything: **what an operator needs in the first 3
seconds is "is something broken?", not "who am I".** The order below is
decreasing severity and increasing reversibility.

```
┌──────────────────────────────────────────────────────────┐
│ ⚠  2 alerts firing                                 [>]   │  ← 1. only shows
│    disk_root  92%  (limit 85%)      critical             │     if there are
│    queue_lag  310s (limit 120s)     warning              │     any
└──────────────────────────────────────────────────────────┘
┌──────────────────────────────────────────────────────────┐
│ Server health                8 ok · 1 degraded     [>]   │  ← 2. rollup,
│  ● whatsapp  degraded — reconnecting                     │     "worst wins"
│  ▸ show the 8 healthy ones                               │
└──────────────────────────────────────────────────────────┘
┌──────────────────────────────────────────────────────────┐
│ Now                                                      │  ← 3. movement
│  ▸ 1 job running · 3 queued                              │
│  ▸ 2 terminal sessions (1 attached)                      │
│  ▸ last deploy: success, 2 h ago                         │
└──────────────────────────────────────────────────────────┘
┌──────────────────────────────────────────────────────────┐
│ CPU 34%   MEM 61%   DISK 92% ⚠                           │  ← 4. saturation
│  ▁▂▃▂▄▅▃▂▁▂▃  (1 h)                                      │
└──────────────────────────────────────────────────────────┘
┌──────────────────────────────────────────────────────────┐
│ Quick actions                                            │  ← 5. what I do
│  [ Attach to last session ]  [ Deploy ]  [ Docker ]      │
└──────────────────────────────────────────────────────────┘
┌──────────────────────────────────────────────────────────┐
│ operator · admin · server time 14:32       [ Sign out ]  │  ← 6. identity
└──────────────────────────────────────────────────────────┘
```

**Why that order:**

1. **Firing alerts** — it is the only card that corresponds to "wake up and fix
   it". PagerDuty puts the incident before everything; the SRE Workbook puts the
   SLI on the landing page. Real data: `alerts[]` from `GET /ops/status` (name,
   severity, state, current value, threshold, unit). **This card only exists
   while there is an alert** — in silence it disappears, and card 2 moves up.
2. **Aggregate health, not nine dots.** `health{}` from `/ops/status` carries
   nine subsystems (audit, ai_router, config, docker, dtach, secrets,
   session_backend, videocall, whatsapp). Showing nine green rows is the "sea of
   dots" the literature tells you to avoid. Showing **"8 ok · 1 degraded" plus
   only the degraded one expanded**, with the healthy ones behind a `▸`, is
   PatternFly's *aggregate status card* with a "worst wins" rollup. A `healthOk`
   boolean already exists in the payload and works as a shortcut.
3. **"Now"** — what is in motion, and the answer to "can I touch the server right
   now?". Queue (`queueRunning`/`queueQueued` from `/ops/status`), sessions
   (`/terminal/sessions`, with the `attached` field the list already uses), last
   deploy (`/deploy/apps`). It is the equivalent of the SRE Workbook's "intended
   changes": before investigating, check whether someone is already working.
4. **Saturation (CPU/MEM/DISK)** comes after, not before. This runs against
   instinct — it is the card every VPS dashboard puts at the top — but the SRE
   Workbook is clear: saturation is a **debugging** metric, not an alerting one;
   it explains *why*, after something has already fired. Putting it on top trains
   the operator to look at a number instead of looking at a problem. **(opinion,
   anchored in [sre.google/workbook](https://sre.google/workbook/monitoring/))**
   Three big numbers plus one sparkline per metric; the series comes from
   `/system/metrics/{cpu,mem,disk}`.
5. **Quick actions** — the "and then what?" of every glance. "Attach to last
   session" is action #1 in this app and today it costs three taps (drawer →
   Terminal → item).
6. **Identity last**, on one line, with the `is_admin` and `server_time` that are
   discarded today. `server_time` is not decoration: a server clock drifting from
   the phone's explains half of the "but I just ran that".

### 3.3 Dashboard behavior rules

- **Adaptive grid, not a fixed column.** `LazyVerticalStaggeredGrid` with
  `StaggeredGridCells.Adaptive(minSize = …)`: on a phone it becomes a single
  column by itself, on a tablet or foldable it becomes two, with no window size
  class `if`
  ([lists](https://developer.android.com/develop/ui/compose/lists)).
- **Surface tone, not shadow.** M3 recommends expressing elevation through tonal
  variation (`surfaceContainer`, `surfaceContainerHigh`) and reserving shadow for
  "protection against the background or encouragement to interact"
  ([m3.material.io/styles/elevation](https://m3.material.io/styles/elevation)).
  With six stacked cards, a shadow on all of them becomes clutter.
- **Auto-refresh plus pull-to-refresh plus "updated at HH:MM".** The `ops.health`
  channel of `/ws/mobile-events` already delivers live health — use it, and show
  the timestamp of the last frame received. Grafana mobile **removed**
  pull-to-refresh on purpose ("it is not supported; the list does not refresh
  when you pull down",
  [grafana.com/docs](https://grafana.com/docs/grafana-cloud/platform/mobile-app/dashboards/));
  I disagree for this case — when the WebSocket drops, the operator needs a way
  to force it, and the gesture is what Android users expect. But **the timestamp
  is mandatory**: without it, "no new data" and "the fetch is stuck" are
  indistinguishable. **(opinion)**
- **Tapping any card navigates to the matching SDUI screen** — alerts →
  `alerts.rules`, health → `system.systemd`, queue → `queue.jobs`, metrics →
  `system.metrics`. It is the drill-down of the *aggregate status card*, and it
  reuses the 24 screens that already exist (§1.1).

### 3.4 What the backend still has to expose

Everything below is small, because the source already exists on the server:

| Missing | Why | Where |
|---|---|---|
| **Instantaneous CPU/MEM/disk value** | `/system/metrics/*` returns a series for a chart; card 4 wants "34%" right now | a new field in `/ops/status`, or `/system/metrics/summary` |
| **Last deploy (status + when)** | `/deploy/apps` is an untyped table row (`schema: {}` in the OpenAPI) | a `last_deploy` field in `/ops/status` |
| **Terminal session count** | today it takes an extra call to `/terminal/sessions` just to count | a `terminal_sessions` field in `/ops/status` |
| **Next scheduled job** | `/scheduler/jobs` exists but is untyped | a `next_scheduled` field in `/ops/status` |
| **Typing for the rows** | every `*Rows` endpoint declares `schema: {}` — the generated client hands back loose JSON, which makes a typed card impossible | give a schema to `/deploy/apps`, `/queue/jobs`, `/scheduler/jobs` |

**Recommendation: one fat `GET /ops/status`, not five calls.** The dashboard is
the opening screen; five round-trips on a mobile network are five chances of a
half-loaded screen. `/ops/status` is already "health, queue and alerts in one
call" — extending it is cheaper and faster than adding endpoints. **(opinion)**

---

## 4. The other pages

### 4.1 Admin — the section picker (the highest return in this document)

Replace the pinned target with a grouped index screen. The owner already asked,
as a general rule, that "anything with a group should become a grouped dropdown"
— and the 25 screens **already come named by group** in their IDs (`docker.*`,
`system.*`, `security.*`, `alerts.*`, plus a few loose ones):

```
┌──────────────────────────────────────────────────────────┐
│ System                                              ▾    │
│   Metrics · Processes · Ports · Services · History       │
├──────────────────────────────────────────────────────────┤
│ Docker                                              ▾    │
│   Containers · Images · Volumes · Networks · Compose     │
├──────────────────────────────────────────────────────────┤
│ Security                                            ▾    │
│   Audit · Sessions · Devices · UFW · AdGuard ·           │
│   Secrets · Users · Savings                              │
├──────────────────────────────────────────────────────────┤
│ Operations                                          ▾    │
│   Queue · Scheduler · Deploys · Alerts · Jira · AI       │
└──────────────────────────────────────────────────────────┘
```

Two requirements:

- **Filter by `capabilities`.** `/me` returns `admin.full`, `docker`,
  `docker.write`, `users`, `users.manage`. A section the user cannot see should
  not appear greyed out — it should not appear. (The server already filters by
  RBAC in `/screens/{id}`; the index has to filter earlier, so it never offers
  something that will come back 403.)
- **Remember the last section opened** and return to it. The operator has two or
  three screens they use constantly; forcing them through the index every time is
  a tax. **(opinion)**

### 4.2 Notifications

Today this is the preferences screen (`/notify/preferences`). What is missing is
the **history**: what the server sent, when, and what happened afterwards.
Datadog calls this a *Notification Center* and describes it as the piece that
avoids "switching to another device, authenticating, navigating to the source"
([datadoghq.com/blog](https://www.datadoghq.com/blog/mobile-app-reduce-mttr/)).
The three channels already exist (`vpsm_deploy`, `vpsm_alerts`, `vpsm_inbox`),
with the right importance levels.

Two high-value improvements:

- **Direct action on the notification.** PagerDuty acknowledges an incident with
  a long-press on the push, without opening the app. Here the equivalent is "view
  deploy log" and "silence this rule for 1 h" as notification `Action`s.
  `ActionableNotificationBuilder` already exists — what is missing is the
  vocabulary of actions.
- **A critical alert that breaks through Do Not Disturb.** It is the only case
  that justifies it. The documented rules: on Android, each "Mode" has its **own**
  allow list
  ([support.pagerduty.com](https://support.pagerduty.com/main/docs/mobile-app-settings));
  Datadog found that inside a **Work Profile the app cannot break through
  DND/volume** and recommends installing it in the personal profile
  ([docs.datadoghq.com](https://docs.datadoghq.com/incident_response/on-call/guides/configure-mobile-device-for-on-call/)).
  Worth writing that on the preferences screen, once, instead of letting the
  operator discover it during an on-call shift.

### 4.3 Terminal (session list)

`SessionListScreen` is structurally good (4 distinct states, no blank screen).
Two additions:

- **A preview of each session's last line.** `GET /terminal/scrollback` already
  exists; one truncated line of mono turns a list of names into a list of
  contexts ("which one was `deploy-2`?"). **(opinion)**
- **Destructive action with the right amount of friction.** Killing a session is
  destructive and irreversible. GitLab Pajamas gives the graduated rule: medium
  severity and reversible → one simple extra step; hard to undo or with permanent
  loss → **a modal with a danger button**, and typing the name **when the action
  removes nested resources**
  ([design.gitlab.com](https://design.gitlab.com/patterns/destructive-actions/)).
  DigitalOcean applies exactly that: destroying a plain Droplet is one click; the
  type-the-name field **only appears when snapshots/volumes will die too**
  ([docs.digitalocean.com](https://docs.digitalocean.com/products/droplets/how-to/destroy/)).
  To kill a terminal session: **a modal with a danger button, no typing the
  name.** For `docker prune` or removing a user: **type the name.**
  And from NN/g: never a generic "Are you sure?" — label the button with the verb
  ("End session", not "OK"), and do not make the destructive option the default
  ([nngroup.com](https://www.nngroup.com/articles/confirmation-dialog/)).
- **Swipe is not confirmation.** NN/g warns that a swipe on its own should not
  confirm a destructive action
  ([contextual-swipe](https://www.nngroup.com/articles/contextual-swipe/)).
  PagerDuty uses swipe-to-ack because **ack is reversible** ("Unacknowledge"
  exists). Here: a swipe may attach, never kill.

### 4.4 Files, WhatsApp, Call, Licenses

These are off the critical path. One observation only: **Licenses should not
share the drawer with Terminal.** It is an attribution obligation, it has to be
reachable, it does not have to compete for space — Android says the same about
Settings: "it is typically not a top-level navigation destination… if there is a
drawer, place it after all the other items"
([layout-and-nav-patterns](https://developer.android.com/design/ui/mobile/guides/layout-and-content/layout-and-nav-patterns)).

---

## 5. Empty states — the difference between informing and teaching

NN/g gives three guidelines, and all three apply here
([empty-state-interface-design](https://www.nngroup.com/articles/empty-state-interface-design/)):

1. **Communicate system status** — without context, the user cannot tell whether
   it is loading, whether it errored, or whether there genuinely is nothing. And
   the opposite mistake is "particularly harmful": showing "No records"
   **before** loading has finished.
2. **Teach** the feature in context.
3. **Give a direct path** to the key task — they explicitly criticize messages
   that say *what* to do without saying *how*.

Two sources disagree about where to put the button, and the disagreement matters:
the **Apple HIG** recommends a button or link inside the empty area itself; **IBM
Carbon** recommends the opposite — "keep the CTA in the same place it always is,
populated or empty, so the user is not confused as they learn the system"
([carbondesignsystem.com](https://carbondesignsystem.com/patterns/empty-states-pattern/)).
**For this app, follow Carbon:** it is a dense daily tool, not a discovery app;
the operator will memorize where things are, and a button that moves with state
gets in the way of that memory. **(opinion)**

And Polaris, on tone: "never make the user feel unsuccessful or at fault for not
having used a feature"
([polaris.shopify.com](https://polaris.shopify.com/components/empty-state)).

**Concretely, in this codebase:**

| Screen | Today | Should become |
|---|---|---|
| Home / `HomeUiState.Empty` | "The server responded, but returned no associated email" | Should not exist: a dashboard is never empty — no alerts is good news. Replace with **"No alerts firing. Last check at 14:32."** with the rest of the cards intact |
| Sessions / `Empty` | "No sessions yet. Attach to a new session by typing a name below." | Already obeys the 3 guidelines. Improve it with **suggested names** (`main`, `deploy`, `logs`) — "typing is the last resort" |
| Empty queue | — | "Nothing queued. Jobs you trigger will show up here." |
| Admin / a section with no rows | the SDUI generic | The SDUI needs an `empty_text` per screen, written by the server — whoever generated the screen is the one who knows why `docker.volumes` is empty |

And NN/g's warning about "premature emptiness" holds as a coding rule: **no
`Empty` state may be rendered while `Loading` is still possible.**
`SessionListUiState` already separates the four states correctly; the SDUI needs
the same discipline.

---

## 6. Navigation: the drawer gives up the work destinations

The official Android docs are unambiguous, and they give the number:

> "The navigation bar can contain **three to five** navigation destinations of
> the same hierarchical level." / "While the navigation drawer can contain
> **more than five** destinations, the pattern isn't as ideal as the navigation
> bar. This is because users need to **reach the top bar** at compact sizes."
> — [layout-and-nav-patterns](https://developer.android.com/design/ui/mobile/guides/layout-and-content/layout-and-nav-patterns)

The reason is ergonomic: the hamburger sits in the top-left corner, the worst
point on the screen for a thumb. In an app that exists to be used **standing up,
one-handed, away from the desk**, that is a structural defect — and today **all
eight destinations** live behind it.

**Recommendation:** a bottom bar with the work destinations, drawer preserved for
the rest.

```
┌──────────────────────────────────────────────────────────┐
│  ☰   vps-manager                                    ⋮    │
│                                                          │
│                      [ content ]                         │
│                                                          │
├──────────────────────────────────────────────────────────┤
│    ⌂ Home        >_ Terminal      ⚙ Admin                │  ← 3 items
└──────────────────────────────────────────────────────────┘
     the drawer (☰) keeps: Call, WhatsApp, Files,
     Notifications, Licenses, Sign out
```

Three items, not five: they are the three the owner named as being the app ("home
is my dashboard", "terminal", "the other pages"). Communication and Files are
episodic — they stay one tap away in the drawer, which is the right place for
what the docs call a secondary destination.

For tablets and foldables, `NavigationSuiteScaffold`
(`androidx.compose.material3.adaptive.navigationsuite`) switches between bottom
bar and *navigation rail* on its own according to the window size class —
breakpoints compact `< 600 dp`, medium `≥ 600 dp`, expanded `≥ 840 dp`
([build-adaptive-navigation](https://developer.android.com/develop/adaptive-apps/guides/build-adaptive-navigation),
[use-window-size-classes](https://developer.android.com/develop/adaptive-apps/guides/use-window-size-classes)).
The docs are explicit: "**avoid using the same bottom bar across all sizes**".
SAP reported going from 379 to 156 lines (-59%) of navigation code after adopting
it
([Android Developers Blog](https://android-developers.googleblog.com/2024/09/sap-integrates-compose-adaptive-api-for-responsive-navigation-ui.html)).

The drawer's current grouping (Operations / Communication / Session / About) is
correct and should survive the change.

---

## 7. Material 3 Expressive: what to adopt, what to ignore

Google published the numbers from its own research: **46 studies, more than
18,000 participants**; with eye-tracking, participants located key elements **up
to four times faster** in the Expressive designs, and there was "a dramatic
erasure of the effect of age" on fixation times
([design.google](https://design.google/library/expressive-material-design-google-research)).
For an app operated under stress, "finding the element faster" is the benefit
that counts — not the visual personality.

State of the library: `androidx.compose.material3` is stable at `1.4.0`; the
Expressive pieces live in the `1.5.0` alphas. A good part has already graduated
out of experimental over the alphas (buttons, `ButtonGroup`, `SplitButton`, FAB
menu, `FloatingToolbar`, flexible app bars, expressive list items,
`MotionScheme`); **`LoadingIndicator` and `MaterialShapes` were promoted and then
reverted** to experimental, so they still require an opt-in
([release notes](https://developer.android.com/jetpack/androidx/releases/compose-material3)).
The app uses Compose BOM `2026.08.00`, so the material is available — but **alpha
is alpha**: adopt per component, not wholesale.

**Adopt:**

| Component | Where, and why |
|---|---|
| **`SplitButton`** | Deploy: primary action plus a menu (dry-run, view log, rollback) without spending a separate overflow |
| **`HorizontalFloatingToolbar`** | The terminal's contextual actions (copy/paste/mouse mode) floating over the grid instead of stacked above it — it was designed for "more positioning versatility" ([docked & floating toolbars](https://github.com/material-components/material-components-android/blob/master/docs/components/DockedFloatingToolbars.md)) |
| **Docked toolbar** | Replaces the bottom app bar, which is being deprecated; it is **shorter** — and height is the scarce resource here |
| **Expressive list items** | The session list and the Admin index |
| **`LoadingIndicator`** (with opt-in) | Meant for waits under 5 s and for pull-to-refresh; replaces most of the app's indeterminate `CircularProgressIndicator`s |

**Ignore, or use sparingly:**

- **"Emphasized" typography at large sizes.** It eats vertical height, which is
  exactly what is in short supply. Emphasized suits the number on a metric card,
  not section titles. **(opinion)**
- **Decorative shape morphing and springs beyond what is needed.** Motion that
  does not communicate state is a battery and attention cost in an on-call app.
  **(opinion)**
- **The classic bottom app bar** — it is being deprecated; if you are going to
  build one, build a docked toolbar.

One note of honesty about the sources: `m3.material.io` is an SPA that does not
serve a page body to automated reading; the citations to it came from indexed
excerpts, with the correct URL, but not from reading the whole page.

---

## 8. Outside the app: widget, tile, notification

For an operator, the value of "without opening the app" is high — it is exactly
what Datadog points to as the real MTTR bottleneck. Three surfaces, in order of
return:

### 8.1 Glance widget — yes, and it is the highest return

A **status** widget: health rollup plus queue plus active alert, in the *Text
layout* format of Google's **Canonical Widget layouts** (title plus short text),
which is the catalogued layout closest to monitoring
([widgets/layouts](https://developer.android.com/design/ui/mobile/guides/widgets/layouts)).

The real limits, and they define the design:

- `updatePeriodMillis` **does not accept less than 30 minutes** — that is a
  platform floor, not a Glance one. For anything more frequent, WorkManager,
  subject to App Standby Buckets
  ([glance-app-widget](https://developer.android.com/develop/ui/compose/glance/glance-app-widget)).
- The widget receiver has a **hard 10 s limit**; any network work goes to
  WorkManager, which calls `update()` when it finishes (same source).
- **Tier 1 of the Widget Quality Tiers** earns discovery in the Play Store's
  "Widget" filter; the criteria are correct cropping, filling the bounds,
  properly sized touch targets, contrast, dynamic color and an automatic preview
  ([Introducing Widget Quality Tiers](https://android-developers.googleblog.com/2025/03/introducing-widget-quality-tiers.html)).
- **Lock screen** (Android 16 QPR2+): no separate quality bar; but if the widget
  launches an Activity while the device is locked, either the user authenticates
  or the Activity declares `android:showWhenLocked="true"`
  ([FAQ](https://android-developers.googleblog.com/2025/03/widgets-on-lock-screen-faq.html)).

**Design consequence:** with a 30 min floor, the widget **is not live
monitoring** — it is a summary. What warns you in seconds is push (FCM, which
already works). Always write the **timestamp of the data** on the widget, so it
does not lie. **(opinion)**

### 8.2 Quick Settings tile — yes, exactly one

A **queue/deploy** tile: it shows "idle" / "1 running" and opens the operations
panel on tap. Implemented with `TileService`; Android 13+ lets you
**programmatically ask** the user to add the tile via `requestAddTileService()`,
skipping the manual Quick Settings editing flow
([quicksettings-tiles](https://developer.android.com/develop/ui/views/quicksettings-tiles)).
Worth offering once, after the first deploy done from the phone.

### 8.3 Persistent notification — only while work is in progress

**Do not** keep a permanent foreground service. The rules have hardened and it is
not worth it:

- Android 13: `POST_NOTIFICATIONS` at runtime; if denied, the FGS runs but **the
  notification does not appear**
  ([notification-permission](https://developer.android.com/about/versions/13/changes/notification-permission)).
- Android 14: **`foregroundServiceType` is mandatory** per service, each type with
  its own permission, on pain of `MissingForegroundServiceTypeException`; and
  `setOngoing(true)` notifications became **dismissible by the user**
  ([fgs-types-required](https://developer.android.com/about/versions/14/changes/fgs-types-required)).
- Android 15: time restrictions on `dataSync`; `shortService` runs for ~3 min
  ([foreground-service-types](https://developer.android.com/about/versions/15/changes/foreground-service-types)).

The legitimate use here is **while a deploy is running**: a notification with
progress, wired to the `deploy.<jobID>` channel the app already consumes, closed
when it ends. That also settles a known debt: a deploy started from the phone
currently goes up to 10 minutes with no log.

---

## 9. What NOT to do

Patterns that look good and age badly on a phone. Each with the reason.

1. **Do not build a "sea of green dots" out of the nine subsystems.** Nine green
   rows train the eye to ignore the whole area, and on the day one of them turns
   red it will be ignored along with the rest. Aggregate by severity and show
   only what deviated. ("Worst wins" rollup:
   [site24x7](https://www.site24x7.com/help/statuspage/setup-status-page/component-groups.html);
   *aggregate status card*:
   [PatternFly](https://www.patternfly.org/patterns/dashboard/design-guidelines/).)

2. **Do not put CPU/MEM/disk at the top of the dashboard.** It is the instinct
   and it is wrong: saturation is a debugging metric, not an alerting one
   ([sre.google/workbook](https://sre.google/workbook/monitoring/)). The top is
   for alerts and SLIs; saturation explains *why*, afterwards.

3. **Do not let the key bar take space while a physical keyboard is attached.**
   It is Termux's years-old open complaint (#556, #3121) and a documented JuiceSSH
   bug ([FAQ](https://juicessh.com/faq/keyboard-doesnt-automatically-hide-when-bluetooth-keyboard-is-attached)).
   Blink solved it in 2016 by hiding them automatically.

4. **Do not build a horizontally scrollable bar with more keys than fit.** That
   is what the app does today with nine keys. A key that requires scrolling is a
   key that does not exist under pressure. Seven per row (Termux), or double the
   capacity through swipe-up.

5. **Do not spend a whole 48 dp row on a button that only says "Hide keys".**
   Nor on "Mode: text selection", nor on "Font size: 14sp". Permanent chrome for
   an episodic control is this screen's structural mistake.

6. **Do not use a swipe as confirmation of a destructive action.** NN/g is
   explicit ([contextual-swipe](https://www.nngroup.com/articles/contextual-swipe/)).
   PagerDuty can use swipe-to-resolve because the action is reversible; killing a
   session and `docker prune` are not.

7. **Do not scale friction uniformly.** Asking for the name to be typed on every
   destructive action becomes a reflex and stops protecting anything — NN/g warns
   about this explicitly
   ([confirmation-dialog](https://www.nngroup.com/articles/confirmation-dialog/)).
   Friction proportional to damage, as in GitLab Pajamas and DigitalOcean.

8. **Do not write "Are you sure?" on any button.** Label it with the verb: "End
   session", "Remove volume". Same source.

9. **Do not keep the drawer as the only navigation.** The Android docs advise
   against it on compact screens, with a named ergonomic reason: "users need to
   reach the top bar"
   ([layout-and-nav-patterns](https://developer.android.com/design/ui/mobile/guides/layout-and-content/layout-and-nav-patterns)).

10. **Do not show an empty state before loading has finished.** NN/g calls that
    "particularly harmful"
    ([empty-state-interface-design](https://www.nngroup.com/articles/empty-state-interface-design/)).

11. **Do not promise real time in a widget.** `updatePeriodMillis` has a 30 min
    floor. A widget that looks live and is 29 minutes stale is worse than an
    honest widget with a timestamp.

12. **Do not adopt Material 3 Expressive wholesale.** `LoadingIndicator` and
    `MaterialShapes` were already promoted and **reverted** to experimental
    ([release notes](https://developer.android.com/jetpack/androidx/releases/compose-material3)).
    Adopt per component, with an explicit opt-in where required.

13. **Do not rely on pinch for font size in the terminal.** The decision already
    recorded in the code is right: it collides with selection and mouse-reporting
    on the same surface. Blink uses pinch because it separates its gestures
    differently; here, it does not work.

14. **Do not replicate the desktop's "aggregate everything".** The web panel has
    room to show it all; the phone screen does not. High density is good for data
    and bad for alerts — Material 3 itself warns that compact density "makes
    alerts and messages hard to notice"
    ([density](https://m3.material.io/foundations/layout/understanding-layout/density)).

---

## 10. Prioritized list of changes

Ordered by (value to the operator) ÷ (effort). **S** = small (≤ 1 day),
**M** = medium (2–4 days), **L** = large (≥ 1 week).

### Wave 1 — gives back screen and content, almost for free

| # | Change | Effort |
|---|---|---|
| 1 | **Section picker in Admin**: a grouped index of the 25 SDUI screens, filtered by `capabilities`, remembering the last one opened. Removes the pinned `scheduler.jobs` target | S |
| 2 | **Take the 3 permanent rows out of the terminal**: font → overflow/bottom sheet; mouse mode → icon in the TopAppBar; scrollback → two-finger gesture. Gives back ~144 dp | S |
| 3 | **Connection banner only while disconnected** | S |
| 4 | **Home shows `is_admin`, `capabilities` and `server_time`** (already received and discarded) | S |
| 5 | **Empty states rewritten** on the existing screens, following NN/g's 3 guidelines, with the CTA in a fixed position (Carbon) | S |

### Wave 2 — the terminal becomes what the owner asked for

| # | Change | Effort |
|---|---|---|
| 6 | **Three-state key bar** with a handle (32 dp collapsed with 4 emergency keys / 40 dp one row / 80 dp two rows), 7 keys per row | M |
| 7 | **Swipe-up = second function** on every key, with the map from §2.2 | M |
| 8 | **Auto-collapse with a physical keyboard** (`hardKeyboardHidden`), reversible through the handle | S |
| 9 | **Long-press repeats** arrows and backspace, 80 ms | S |
| 10 | **The bar's second page = a command field** (horizontal swipe) | M |
| 11 | **Last-line preview** for every session in the list | S |
| 12 | **Macro drawer** with 6 fixed, useful sequences | M |

### Wave 3 — Home becomes a dashboard

| # | Change | Effort |
|---|---|---|
| 13 | **Backend: fatten `GET /ops/status`** with the instantaneous CPU/MEM/disk value, last deploy, session count and next scheduled job | M |
| 14 | **Dashboard with the 6 cards** in the order from §3.2, in an adaptive `LazyVerticalStaggeredGrid`, surface tone instead of shadow | L |
| 15 | **Live through the `ops.health` channel** plus pull-to-refresh plus an "updated at HH:MM" stamp | M |
| 16 | **Drill-down**: each card navigates to the matching SDUI screen | S |
| 17 | **Type the rows** of `/deploy/apps`, `/queue/jobs`, `/scheduler/jobs` in the OpenAPI (today `schema: {}`) | M |

### Wave 4 — shell and external surfaces

| # | Change | Effort |
|---|---|---|
| 18 | **Bottom bar with 3 items** (Home, Terminal, Admin); the drawer keeps the rest | M |
| 19 | **`NavigationSuiteScaffold`** to adapt into a rail at ≥ 600 dp | S |
| 20 | **Glance widget** for status (health + queue + alert), canonical Text layout, WorkManager, timestamp | M |
| 21 | **Quick Settings tile** for queue/deploy, with `requestAddTileService()` offered after the first deploy | M |
| 22 | **Progress notification during a deploy**, wired to the `deploy.<jobID>` channel (also settles the 10-min-without-log debt) | M |
| 23 | **Inline notification actions** (view log, silence rule for 1 h) plus a note about DND per Mode and Work Profile on the preferences screen | M |

### Wave 5 — getting ahead

| # | Change | Effort |
|---|---|---|
| 24 | **Server-side scrollback search**, with navigable results. None of the 8 apps surveyed has this on mobile | L |
| 25 | **Graduated destructive confirmation** (modal with a danger button / type the name when nested resources are removed) applied uniformly across the SDUI | M |
| 26 | **`SplitButton` in Deploy** and **`FloatingToolbar`** for the terminal actions | M |

---

## Appendix — sources, grouped

**Mobile terminals (source code and official docs)**
[Termux `activity_termux.xml`](https://github.com/termux/termux-app/blob/master/app/src/main/res/layout/activity_termux.xml) ·
[`TermuxActivity`](https://github.com/termux/termux-app/blob/master/app/src/main/java/com/termux/app/TermuxActivity.java) ·
[`TermuxPropertyConstants`](https://github.com/termux/termux-app/blob/master/termux-shared/src/main/java/com/termux/shared/termux/settings/properties/TermuxPropertyConstants.java) ·
[`ExtraKeysInfo`](https://github.com/termux/termux-app/blob/master/termux-shared/src/main/java/com/termux/shared/termux/extrakeys/ExtraKeysInfo.java) ·
[`ExtraKeysView`](https://github.com/termux/termux-app/blob/master/termux-shared/src/main/java/com/termux/shared/termux/extrakeys/ExtraKeysView.java) ·
[`TerminalToolbarViewPager`](https://github.com/termux/termux-app/blob/master/app/src/main/java/com/termux/app/terminal/io/TerminalToolbarViewPager.java) ·
[Blink docs](https://docs.blink.sh/) ·
[Blink customize](https://github.com/blinksh/docs/blob/master/src/pages/basics/customize.md) ·
[Prompt keyboard](https://help.panic.com/prompt/prompt-keyboard/) ·
[Termius mobile terminal](https://docs.termius.com/terminal/mobile-terminal) ·
[Termius: search is desktop-only](https://support.termius.com/hc/en-us/articles/900006226406-How-do-I-search-in-the-terminal-) ·
[ShellFish snippets](https://secureshellfish.app/help/snippets) ·
[JuiceSSH FAQ](https://juicessh.com/faq/keyboard-doesnt-automatically-hide-when-bluetooth-keyboard-is-attached) ·
[510 ConnectBot](https://www.five-ten-sg.com/510Connectbot/VirtualKeyboard.html)

**Operations panels and dashboards**
[Google SRE Workbook — Monitoring](https://sre.google/workbook/monitoring/) ·
[PagerDuty mobile](https://support.pagerduty.com/main/docs/mobile-app) ·
[PagerDuty settings/DND](https://support.pagerduty.com/main/docs/mobile-app-settings) ·
[Datadog mobile / MTTR](https://www.datadoghq.com/blog/mobile-app-reduce-mttr/) ·
[Datadog on-call on mobile](https://docs.datadoghq.com/incident_response/on-call/guides/configure-mobile-device-for-on-call/) ·
[Grafana mobile dashboards](https://grafana.com/docs/grafana-cloud/platform/mobile-app/dashboards/) ·
[PatternFly dashboard patterns](https://www.patternfly.org/patterns/dashboard/design-guidelines/) ·
[Statuspage component groups](https://support.atlassian.com/statuspage/docs/create-a-component-group/) ·
[Site24x7 component group rollup](https://www.site24x7.com/help/statuspage/setup-status-page/component-groups.html) ·
[Portainer mobile #9524](https://github.com/orgs/portainer/discussions/9524) ·
[Dokploy mobile #3172](https://github.com/Dokploy/dokploy/issues/3172) ·
[Linode discontinued its app](https://www.linode.com/community/questions/20748/do-we-have-any-mobile-application-for-linode)

**General UX**
[NN/g — empty states](https://www.nngroup.com/articles/empty-state-interface-design/) ·
[NN/g — confirmation dialogs](https://www.nngroup.com/articles/confirmation-dialog/) ·
[NN/g — contextual swipe](https://www.nngroup.com/articles/contextual-swipe/) ·
[NN/g — utilize available screen space](https://www.nngroup.com/articles/utilize-available-screen-space/) ·
[NN/g — mobile tables](https://www.nngroup.com/articles/mobile-tables/) ·
[Carbon — empty states](https://carbondesignsystem.com/patterns/empty-states-pattern/) ·
[Polaris — empty state](https://polaris.shopify.com/components/empty-state) ·
[GitLab Pajamas — destructive actions](https://design.gitlab.com/patterns/destructive-actions/) ·
[DigitalOcean — destroy a Droplet](https://docs.digitalocean.com/products/droplets/how-to/destroy/)

**Android / Material 3**
[Layout and navigation patterns](https://developer.android.com/design/ui/mobile/guides/layout-and-content/layout-and-nav-patterns) ·
[Adaptive navigation](https://developer.android.com/develop/adaptive-apps/guides/build-adaptive-navigation) ·
[Window size classes](https://developer.android.com/develop/adaptive-apps/guides/use-window-size-classes) ·
[Compose Material3 release notes](https://developer.android.com/jetpack/androidx/releases/compose-material3) ·
[Material 3 Expressive research](https://design.google/library/expressive-material-design-google-research) ·
[Docked & floating toolbars](https://github.com/material-components/material-components-android/blob/master/docs/components/DockedFloatingToolbars.md) ·
[M3 — elevation](https://m3.material.io/styles/elevation) ·
[M3 — density](https://m3.material.io/foundations/layout/understanding-layout/density) ·
[Lazy lists and grids](https://developer.android.com/develop/ui/compose/lists) ·
[48 dp touch target](https://developer.android.com/guide/topics/ui/accessibility/apps) ·
[Compose accessibility defaults](https://developer.android.com/develop/ui/compose/accessibility/api-defaults) ·
[Insets and the IME](https://developer.android.com/develop/ui/compose/system/insets-ui) ·
[WindowInsetsRulers](https://developer.android.com/develop/ui/compose/system/evaluate-rulers) ·
[Physical keyboard in Compose](https://developer.android.com/develop/ui/compose/touch-input/keyboard-input/commands) ·
[Glance App Widget](https://developer.android.com/develop/ui/compose/glance/glance-app-widget) ·
[Widget Quality Tiers](https://android-developers.googleblog.com/2025/03/introducing-widget-quality-tiers.html) ·
[Widgets on the lock screen](https://android-developers.googleblog.com/2025/03/widgets-on-lock-screen-faq.html) ·
[Canonical widget layouts](https://developer.android.com/design/ui/mobile/guides/widgets/layouts) ·
[Quick Settings tiles](https://developer.android.com/develop/ui/views/quicksettings-tiles) ·
[Android 13 — POST_NOTIFICATIONS](https://developer.android.com/about/versions/13/changes/notification-permission) ·
[Android 14 — FGS types](https://developer.android.com/about/versions/14/changes/fgs-types-required) ·
[Android 15 — FGS](https://developer.android.com/about/versions/15/changes/foreground-service-types)
