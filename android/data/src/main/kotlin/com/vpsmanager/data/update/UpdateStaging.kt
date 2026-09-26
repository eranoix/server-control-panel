package com.vpsmanager.data.update

import android.content.Context
import android.os.storage.StorageManager
import java.io.File
import java.io.IOException

/** Desfecho de [UpdateStaging.reserve]. */
sealed interface StorageReservation {

    /** There is space (and it was reserved with the system, where the OS allowed it). */
    data object Reserved : StorageReservation

    /**
     * It does not fit. [missingBytes] is what is short — the only piece of
     * information the device's owner can act on, so it reaches the screen in MB.
     */
    data class NotEnoughSpace(val missingBytes: Long) : StorageReservation

    /**
     * The space query failed (a volume with no UUID, the OS refused). We carry
     * on deliberately: refusing a perfectly good update because a READ did not
     * work would be trading a real problem for an invented one. A genuinely
     * full disk is still caught by `hpatchz` and by the installer.
     */
    data class Unknown(val reason: String) : StorageReservation
}

/**
 * The place on disk where the update is assembled.
 *
 * ### `filesDir`, NEVER `cacheDir`
 * Three forces erase `cacheDir` without warning: the system's cleanup under
 * space pressure, app hibernation (which zeroes the cache of unused apps), and
 * — the most treacherous — `PackageInstaller`'s own pre-allocation, which frees
 * space by evicting app caches BEFORE opening the session. In other words:
 * downloading 10 MB into `cacheDir` and then asking the installer for space can
 * delete precisely the file just downloaded. `filesDir` is subject to none of
 * the three.
 *
 * `open` (like `TransferRepository`) so a test can make [reserve] answer "out
 * of space" without having to fill the CI machine's disk.
 *
 * ### Names derived from content
 * The artifact and the rebuilt APK are named by their own SHA-256. Two good
 * consequences come free: an interrupted download is found again by name on the
 * next attempt (which is what makes resuming possible after the process dies),
 * and an artifact from an earlier version is never confused with the current
 * one.
 */
open class UpdateStaging(context: Context) {

    private val appContext = context.applicationContext

    /** `filesDir/atualizacoes`. Criado sob demanda. */
    val dir: File
        get() = File(appContext.filesDir, DIR_NAME).also { if (!it.isDirectory) it.mkdirs() }

    /** The downloaded `.hdiff` (patch or full), named by its hash. */
    fun artifactFile(sha256: String): File = File(dir, "$sha256.hdiff")

    /** The rebuilt APK, named by the hash the server declared for it. */
    fun rebuiltApkFile(apkSha256: String): File = File(dir, "$apkSha256.apk")

    /**
     * An empty base for the full path. The "full" artifact is a `.hdiff` too
     * (`hdiffz` against an empty base), so the device has just ONE code path:
     * always `hpatchz`, changing only which file goes in as the base.
     */
    fun emptyBaseFile(): File = File(dir, EMPTY_BASE_NAME).also {
        if (!it.isFile || it.length() != 0L) {
            it.delete()
            it.createNewFile()
        }
    }

    /**
     * Asks the system for [bytes] on [dir]'s volume BEFORE starting the
     * download.
     *
     * `getAllocatableBytes` is larger than the naive "free space": it counts
     * what the system could free by evicting other apps' caches. And
     * `allocateBytes` actually performs that eviction and reserves the quota,
     * so the download does not die half-way because another app filled the
     * disk.
     */
    open fun reserve(bytes: Long): StorageReservation {
        if (bytes <= 0) return StorageReservation.Reserved
        val manager = appContext.getSystemService(StorageManager::class.java)
            ?: return StorageReservation.Unknown("StorageManager unavailable")
        return try {
            val uuid = manager.getUuidForPath(dir)
            val allocatable = manager.getAllocatableBytes(uuid)
            if (allocatable < bytes) {
                StorageReservation.NotEnoughSpace(bytes - allocatable)
            } else {
                manager.allocateBytes(uuid, bytes)
                StorageReservation.Reserved
            }
        } catch (e: IOException) {
            StorageReservation.Unknown(e.message ?: e.toString())
        } catch (e: SecurityException) {
            StorageReservation.Unknown(e.message ?: e.toString())
        } catch (e: UnsupportedOperationException) {
            // Robolectric and some volumes do not implement the query.
            StorageReservation.Unknown(e.message ?: e.toString())
        }
    }

    /**
     * Deletes everything in [dir] that is not in [keep].
     *
     * Called when the target changes (a new release came out mid-way) and after
     * installing. It is NOT called when a download fails: the partial file is
     * precisely what the next attempt resumes from.
     */
    fun sweep(keep: Set<File>) {
        val keepPaths = keep.map { it.absolutePath }.toSet()
        dir.listFiles()?.forEach { file ->
            if (file.absolutePath !in keepPaths) file.delete()
        }
    }

    private companion object {
        const val DIR_NAME = "atualizacoes"
        const val EMPTY_BASE_NAME = "base-vazia"
    }
}
