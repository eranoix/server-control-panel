package dev.servercontrolpanel.data.update

import android.content.Context
import android.os.storage.StorageManager
import java.io.File
import java.io.IOException

sealed interface StorageReservation {

    data object Reserved : StorageReservation

    data class NotEnoughSpace(val missingBytes: Long) : StorageReservation

    data class Unknown(val reason: String) : StorageReservation
}

open class UpdateStaging(context: Context) {

    private val appContext = context.applicationContext

    val dir: File
        get() = File(appContext.filesDir, DIR_NAME).also { if (!it.isDirectory) it.mkdirs() }

    fun artifactFile(sha256: String): File = File(dir, "$sha256.hdiff")

    fun rebuiltApkFile(apkSha256: String): File = File(dir, "$apkSha256.apk")

    fun emptyBaseFile(): File = File(dir, EMPTY_BASE_NAME).also {
        if (!it.isFile || it.length() != 0L) {
            it.delete()
            it.createNewFile()
        }
    }

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
            StorageReservation.Unknown(e.message ?: e.toString())
        }
    }

    fun sweep(keep: Set<File>) {
        val keepPaths = keep.map { it.absolutePath }.toSet()
        dir.listFiles()?.forEach { file ->
            if (file.absolutePath !in keepPaths) file.delete()
        }
    }

    private companion object {
        const val DIR_NAME = "updates"
        const val EMPTY_BASE_NAME = "empty-base"
    }
}
