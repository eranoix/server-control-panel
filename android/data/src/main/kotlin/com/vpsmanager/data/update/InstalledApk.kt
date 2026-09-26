package com.vpsmanager.data.update

import android.content.Context
import com.vpsmanager.patchengine.ApkPatcher
import java.io.File
import java.io.IOException

/**
 * The APK running right now, identified by its bytes. The incremental channel is
 * keyed by its SHA-256, never `versionCode`: two builds with the same
 * `versionCode` differ in bytes, and a wrong patch yields a corrupted file.
 */
sealed interface InstalledApkResult {

    /** [file] exists, is readable, and [sha256] is the hash of its bytes. */
    data class Ok(val file: File, val sha256: String) : InstalledApkResult

    /**
     * The installed APK could not be identified. Not a reason to give up: with no
     * base the server returns `patch: null` plus the full artifact.
     */
    data class Unavailable(val reason: String) : InstalledApkResult
}

/**
 * Reads and hashes the installed APK.
 *
 * The path is read every time because `applicationInfo.sourceDir` contains
 * random segments that change on every (re)install. The hash (0.2 to 0.5 s for
 * 31 MB) is cached by path + size + mtime, so it invalidates itself when the APK changes.
 */
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

        // Keep a single entry: older ones describe APKs no longer installed.
        prefs.edit().clear().putString(identity, hash).apply()
        return InstalledApkResult.Ok(file, hash)
    }

    private companion object {
        const val PREFS_NAME = "vpsm_update_base"
    }
}
