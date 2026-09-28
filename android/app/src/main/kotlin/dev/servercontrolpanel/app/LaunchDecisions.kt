package dev.servercontrolpanel.app

internal fun shouldShowDiagnosticScreen(lastCrash: String?, initFailures: List<String>): Boolean =
    lastCrash != null || initFailures.isNotEmpty()

internal fun shouldSeedDefaultServer(currentBaseUrl: String?, defaultServerUrl: String): Boolean =
    currentBaseUrl == null && defaultServerUrl.isNotBlank()

internal enum class LaunchDestination {
    AuthGate,

    Home,
}

internal fun decideLaunchDestination(hasServerConfigured: Boolean, hasSession: Boolean): LaunchDestination =
    if (hasServerConfigured && hasSession) LaunchDestination.Home else LaunchDestination.AuthGate
