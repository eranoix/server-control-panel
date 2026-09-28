package dev.servercontrolpanel.data.update

import android.content.Context
import android.content.pm.PackageManager
import dev.servercontrolpanel.data.config.ServerConfigRepository
import kotlinx.coroutines.CoroutineScope

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
        recordDiagnostic = { text -> UpdateDiagnostics.record(appContext, text) },
        manualInstallUrl = { serverConfigRepository.currentBaseUrl()?.let { "$it/android/install" } },
        scope = scope,
    )
}

internal fun installedVersionCode(context: Context): Long = try {
    context.packageManager.getPackageInfo(context.packageName, 0).longVersionCode
} catch (e: PackageManager.NameNotFoundException) {
    0L
}

fun unknownSourcesSettingsIntent(context: Context) =
    ApkInstaller(context).unknownSourcesSettingsIntent()
