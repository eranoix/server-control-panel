package com.vpsmanager.app

/**
 * Pure decision logic behind [MainActivity.onCreate]'s two non-UI branch points, pulled out of
 * that `setContent { }`-entangled function so they can be pinned by a plain JUnit test instead of
 * a Robolectric/Compose one — this app has never run on a real device or emulator, so these two
 * decisions getting the wrong answer on first boot would ship silently.
 */

/**
 * Whether [MainActivity.onCreate] must show [com.vpsmanager.app.DiagnosticoScreen] instead of the
 * normal setup/login/home flow.
 *
 * The operator has no `adb` on a real device — if the previous process died ([lastCrash] is the
 * persisted report from [Bootstrap.installCrashReporter]) or any [Bootstrap.step] failed on this
 * boot ([initFailures]), the diagnostic screen is the ONLY way that information ever reaches them.
 * Either condition alone is enough; both together still show the one screen, not two.
 */
internal fun shouldShowDiagnosticScreen(lastCrash: String?, initFailures: List<String>): Boolean =
    lastCrash != null || initFailures.isNotEmpty()

/**
 * Whether [MainActivity.onCreate] should seed [BuildConfig.DEFAULT_SERVER_URL] into
 * [com.vpsmanager.data.config.ServerConfigRepository] on this boot.
 *
 * Only true when BOTH hold:
 * - [currentBaseUrl] is `null` — a device that already has a server configured is never touched;
 *   this path must NEVER pass `allowRepoint = true` to
 *   [com.vpsmanager.data.config.ServerConfigRepository.configure], so calling it on an already-
 *   configured device would just return
 *   [com.vpsmanager.data.config.ServerConfigRepository.ConfigureServerResult.RepointBlocked] --
 *   this check exists to skip that pointless call, not to enforce the safety itself.
 * - [defaultServerUrl] is not blank — without `vpsmanager.defaultServerUrl` set at build time, the
 *   `BuildConfig` field is an empty string, and seeding an empty URL would just be a different way
 *   of writing "no server configured".
 */
internal fun shouldSeedDefaultServer(currentBaseUrl: String?, defaultServerUrl: String): Boolean =
    currentBaseUrl == null && defaultServerUrl.isNotBlank()

/** What [MainActivity] shows after the diagnostics. */
internal enum class LaunchDestination {
    /** Everything that happens before a session exists — pair, configure, sign in. */
    AuthGate,

    /** O app propriamente dito. */
    Home,
}

/**
 * Decides the first screen from the TWO states it depends on.
 *
 * Before this, `onCreate` asked only `currentBaseUrl() != null` and treated
 * the answer as "ready to use the app". The two states are independent,
 * and collapsing them into one boolean only worked while the app did not
 * embed a default server: as soon as `BuildConfig.DEFAULT_SERVER_URL`
 * started being seeded on first boot ([shouldSeedDefaultServer], a few
 * lines above and in the SAME function), the test turned true on a fresh
 * install — and the app opened straight into `AppNavHost`, with no
 * credentials at all, where every screen hit a 401 and offered a "Try
 * again" that could never work.
 *
 * [hasSession] is the existence of a usable access+refresh pair
 * ([com.vpsmanager.data.auth.SessionState.SignedIn]); [hasServerConfigured]
 * is having an address. Going to Home requires both: a stored session with
 * no server configured (corrupt state, or a partial backup restore) has
 * nobody to talk to, and sending the operator to Home in that case would
 * reproduce the same wall of errors by another route.
 */
internal fun decideLaunchDestination(hasServerConfigured: Boolean, hasSession: Boolean): LaunchDestination =
    if (hasServerConfigured && hasSession) LaunchDestination.Home else LaunchDestination.AuthGate
