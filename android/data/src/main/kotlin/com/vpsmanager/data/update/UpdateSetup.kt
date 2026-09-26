package com.vpsmanager.data.update

import android.content.Context
import android.content.pm.PackageManager
import com.vpsmanager.data.config.ServerConfigRepository
import kotlinx.coroutines.CoroutineScope

/**
 * Builds the production [UpdateCoordinator].
 *
 * It exists so that `:app` never has to name `PackageInstaller`, `ApkPatcher`
 * or `PatchResult` — the module boundary stays "update state goes onto the
 * screen", and `:patch-engine` remains an internal detail of `:data`, exactly
 * as the generated client already is.
 *
 * Follows this project's DI convention (no graph; explicit construction in a
 * `by lazy` on the `Application`) — see `AppSession`.
 */
fun createUpdateCoordinator(
    context: Context,
    serverConfigRepository: ServerConfigRepository,
    scope: CoroutineScope,
): UpdateCoordinator {
    val appContext = context.applicationContext
    val reader = InstalledApkReader(appContext)
    return UpdateCoordinator(
        source = UpdateRepository(serverConfigRepository),
        staging = UpdateStaging(appContext),
        installer = ApkInstaller(appContext),
        readInstalledApk = reader::read,
        installedVersionCode = installedVersionCode(appContext),
        appLabel = appContext.applicationInfo.loadLabel(appContext.packageManager),
        recordDiagnostic = { texto -> UpdateDiagnostics.record(appContext, texto) },
        // The server's own rescue page: when the device refuses to install
        // from here, that is where the owner goes — and not to a search in the
        // browser.
        manualInstallUrl = { serverConfigRepository.currentBaseUrl()?.let { "$it/android/install" } },
        scope = scope,
    )
}

/** The `versionCode` currently running. Zero when the system cannot say — never blocks the offer. */
internal fun installedVersionCode(context: Context): Long = try {
    context.packageManager.getPackageInfo(context.packageName, 0).longVersionCode
} catch (e: PackageManager.NameNotFoundException) {
    0L
}

/** Shortcut to the screen: THIS app's "install unknown apps" switch. */
fun unknownSourcesSettingsIntent(context: Context) =
    ApkInstaller(context).unknownSourcesSettingsIntent()
