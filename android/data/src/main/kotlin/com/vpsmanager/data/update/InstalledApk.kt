package com.vpsmanager.data.update

import android.content.Context
import com.vpsmanager.patchengine.ApkPatcher
import java.io.File
import java.io.IOException

/**
 * The APK running RIGHT NOW, identified by its bytes.
 *
 * The incremental channel is keyed by the SHA-256 of the installed APK, never
 * by `versionCode` — two builds of the same `versionCode` (a rebuild, a
 * re-signing, a different ABI) have different bytes, and applying the wrong
 * patch raises no version error: it produces a corrupted file. See
 * `docs/android-atualizacao-incremental.md`.
 */
sealed interface InstalledApkResult {

    /** [file] exists, is readable, and [sha256] is the hash of its bytes. */
    data class Ok(val file: File, val sha256: String) : InstalledApkResult

    /**
     * The installed APK could not be identified. That is NOT a reason to give
     * up on the update: with no base the server returns `patch: null` plus the
     * full path, which rebuilds the APK from scratch. It is exactly the first
     * rung of the fallback ladder.
     */
    data class Unavailable(val reason: String) : InstalledApkResult
}

/**
 * Reads and hashes the installed APK.
 *
 * ### Why the path is read every time
 * `applicationInfo.sourceDir` is
 * `/data/app/~~<random>/<package>-<random>/base.apk` — both random segments
 * change ON EVERY (re)installation, by Android's design. Storing the path and
 * reusing it after an update points at a directory that no longer exists. That
 * is why [read] asks the context's `ApplicationInfo` every time, and why
 * nothing here persists a path as configuration.
 *
 * ### Why the HASH can be cached
 * Hashing 31 MB costs 0.2 to 0.5 s — little, but paid on every opening of the
 * app and on every periodic check. The cache is keyed by the file's identity
 * (path + size + mtime): since the path changes on every installation, the
 * cache invalidates itself when the APK changes. There is no scenario in which
 * it hands back the hash of a file that is not the current one.
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

        // A single entry: the previous one describes an APK that is no longer
        // installed, and keeping history here would buy nothing.
        prefs.edit().clear().putString(identity, hash).apply()
        return InstalledApkResult.Ok(file, hash)
    }

    private companion object {
        const val PREFS_NAME = "vpsm_update_base"
    }
}
