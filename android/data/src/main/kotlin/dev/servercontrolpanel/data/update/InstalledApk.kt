package dev.servercontrolpanel.data.update

import android.content.Context
import dev.servercontrolpanel.patchengine.ApkPatcher
import java.io.File
import java.io.IOException

sealed interface InstalledApkResult {

    data class Ok(val file: File, val sha256: String) : InstalledApkResult

    data class Unavailable(val reason: String) : InstalledApkResult
}

class InstalledApkReader(
    context: Context,
    private val sha256Of: (File) -> String = ApkPatcher::sha256Of,
) {

    private val appContext = context.applicationContext
    private val prefs by lazy { appContext.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE) }

    fun read(): InstalledApkResult {
        val sourceDir = appContext.applicationInfo?.sourceDir
            ?: return InstalledApkResult.Unavailable("the system did not report the installed APK's path")
        val file = File(sourceDir)
        if (!file.isFile || !file.canRead()) {
            return InstalledApkResult.Unavailable("the installed APK is not readable at $sourceDir")
        }

        val identity = "${file.path}|${file.length()}|${file.lastModified()}"
        prefs.getString(identity, null)?.let { return InstalledApkResult.Ok(file, it) }

        val hash = try {
            sha256Of(file)
        } catch (e: IOException) {
            return InstalledApkResult.Unavailable("failed to read the installed APK: ${e.message}")
        } catch (e: SecurityException) {
            return InstalledApkResult.Unavailable("no permission to read the installed APK")
        }

        prefs.edit().clear().putString(identity, hash).apply()
        return InstalledApkResult.Ok(file, hash)
    }

    private companion object {
        const val PREFS_NAME = "panel_update_base"
    }
}
