package dev.servercontrolpanel.app

// Pure launch decisions for MainActivity.onCreate, kept here so plain JUnit tests can cover them.

/**
 * Whether [MainActivity.onCreate] must show [dev.servercontrolpanel.app.DiagnosticsScreen] instead of the
 * normal flow: when the previous process crashed ([lastCrash]) or any [Bootstrap.step] failed
 * ([initFailures]). Either alone is enough.
 */
internal fun shouldShowDiagnosticScreen(lastCrash: String?, initFailures: List<String>): Boolean =
    lastCrash != null || initFailures.isNotEmpty()

/**
 * Whether [MainActivity.onCreate] should seed [BuildConfig.DEFAULT_SERVER_URL] into
 * [dev.servercontrolpanel.data.config.ServerConfigRepository] on this boot.
 *
 * Only when no server is configured yet ([currentBaseUrl] is `null`) and [defaultServerUrl] is
 * not blank (it is empty when the build property is unset). The seeding path must never pass
 * `allowRepoint = true` to [dev.servercontrolpanel.data.config.ServerConfigRepository.configure]; this
 * check only skips a call that would be blocked anyway.
 */
internal fun shouldSeedDefaultServer(currentBaseUrl: String?, defaultServerUrl: String): Boolean =
    currentBaseUrl == null && defaultServerUrl.isNotBlank()

/** What [MainActivity] shows after the diagnostics. */
internal enum class LaunchDestination {
    /** Everything before a session exists: pair, configure, sign in. */
    AuthGate,

    /** The app itself. */
    Home,
}

/**
 * Decides the first screen from two independent states: [hasServerConfigured] (an address,
 * which may have been seeded by default) and [hasSession] (a usable token pair,
 * [dev.servercontrolpanel.data.auth.SessionState.SignedIn]). Home requires both; otherwise every
 * screen would hit a 401 or have no server to talk to.
 */
internal fun decideLaunchDestination(hasServerConfigured: Boolean, hasSession: Boolean): LaunchDestination =
    if (hasServerConfigured && hasSession) LaunchDestination.Home else LaunchDestination.AuthGate
