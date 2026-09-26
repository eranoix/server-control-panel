package com.vpsmanager.data.update

import android.content.Context
import android.os.storage.StorageManager
import java.io.File
import java.io.IOException

/** Outcome of [UpdateStaging.reserve]. */
sealed interface StorageReservation {

    /** There is space, reserved with the system where the OS allowed it. */
    data object Reserved : StorageReservation

    /** It does not fit. [missingBytes] is shown on screen in MB, since the owner can act on it. */
    data class NotEnoughSpace(val missingBytes: Long) : StorageReservation

    /**
     * The space query failed (volume without UUID, OS refused). We carry on: a
     * genuinely full disk is still caught by `hpatchz` and the installer.
     */
    data class Unknown(val reason: String) : StorageReservation
}

/**
 * The place on disk where the update is assembled.
 *
 * Uses `filesDir`, NEVER `cacheDir`: cache can be wiped by low-space cleanup,
 * app hibernation, and `PackageInstaller`'s own pre-allocation, which may evict
 * the very file just downloaded.
 *
 * Files are named by their SHA-256, so an interrupted download is found again on
 * the next attempt (resume after process death) and an artifact from another
 * version is never mistaken for the current one. `open` so tests can fake [reserve].
 */
open class UpdateStaging(context: Context) {

    private val appContext = context.applicationContext

    /** `filesDir/atualizacoes`, created on demand. */
    val dir: File
        get() = File(appContext.filesDir, DIR_NAME).also { if (!it.isDirectory) it.mkdirs() }

    /** The downloaded `.hdiff` (patch or full), named by its hash. */
    fun artifactFile(sha256: String): File = File(dir, "$sha256.hdiff")

    /** The rebuilt APK, named by the hash the server declared for it. */
    fun rebuiltApkFile(apkSha256: String): File = File(dir, "$apkSha256.apk")

    /**
     * An empty base for the full path. The full artifact is also a `.hdiff`
     * (against an empty base), so the device has one code path: always `hpatchz`.
     */
    fun emptyBaseFile(): File = File(dir, EMPTY_BASE_NAME).also {
        if (!it.isFile || it.length() != 0L) {
            it.delete()
            it.createNewFile()
        }
    }

    /**
     * Asks the system for [bytes] on [dir]'s volume BEFORE the download.
     * `getAllocatableBytes` includes cache the system could evict, and
     * `allocateBytes` performs that eviction and reserves the space so the
     * download does not die half-way.
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
     * Deletes everything in [dir] that is not in [keep]. Called when the target
     * changes and after installing, never after a failed download, since the
     * partial file is what the next attempt resumes from.
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
