package dev.servercontrolpanel.feature.files.transfer

import android.content.ContentValues
import android.content.Context
import android.net.Uri
import android.provider.MediaStore
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import dev.servercontrolpanel.data.files.DownloadChunkResult
import dev.servercontrolpanel.data.files.TransferRepository
import java.io.FileNotFoundException
import java.io.IOException

/**
 * Downloads a server file into the system's shared MediaStore Downloads
 * collection, resuming from wherever it left off after a
 * cancellation, retry or process death.
 *
 * The resume offset is never tracked as a separately-persisted byte counter:
 * it is read back from the MediaStore entry's own on-disk size
 * (`ParcelFileDescriptor.statSize`) every time this worker starts, which is
 * self-healing by construction -- there is no separate counter that can
 * ever drift from what is actually written to disk. Only the MediaStore
 * [Uri] itself needs to survive across attempts, via [TransferStateStore].
 */
// @JvmOverloads: WorkManager's default factory instantiates by reflection,
// looking for exactly `(Context, WorkerParameters)`, which parameters with
// default values in Kotlin do NOT generate. Without this, "Could not create
// Worker" and FAILED before the first line of doWork — see the KDoc of
// UploadWorker.
class DownloadWorker @JvmOverloads constructor(
    context: Context,
    params: WorkerParameters,
    private val transferRepository: TransferRepository = TransferRepository(),
    private val stateStore: TransferStateStore = TransferStateStore(context),
) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result {
        val workName = inputData.getString(KEY_WORK_NAME) ?: return Result.failure()
        val serverPath = inputData.getString(KEY_SERVER_PATH) ?: return Result.failure()
        val filename = inputData.getString(KEY_FILENAME) ?: return Result.failure()
        val mimeType = inputData.getString(KEY_MIME_TYPE) ?: "application/octet-stream"
        val totalBytes = inputData.getLong(KEY_TOTAL_BYTES, -1L).takeIf { it > 0 }

        val mediaUri = resolveTargetUri(workName, filename, mimeType)
            ?: return Result.failure(workDataOf(KEY_ERROR_REASON to "Could not create the destination file."))

        val alreadyWritten = statSizeOrZero(mediaUri)

        setForeground(
            TransferNotifications.foregroundInfo(
                context = applicationContext,
                workId = id,
                title = "Downloading $filename",
                percent = percentOf(alreadyWritten, totalBytes),
            ),
        )

        var bytesWritten = alreadyWritten
        var failureReason: String? = null

        try {
            val opened = applicationContext.contentResolver.openOutputStream(mediaUri, "wa")
            if (opened == null) {
                failureReason = "Could not open the destination file."
            } else {
                opened.use { output ->
                    transferRepository.downloadRange(serverPath, alreadyWritten).collect { chunk ->
                        when (chunk) {
                            is DownloadChunkResult.Bytes -> if (!chunk.isLast) {
                                output.write(chunk.data)
                                bytesWritten += chunk.data.size
                                setProgress(workDataOf(KEY_PERCENT to percentOf(bytesWritten, totalBytes)))
                            }
                            is DownloadChunkResult.Error -> failureReason = chunk.reason
                        }
                    }
                }
            }
        } catch (e: IOException) {
            failureReason = "Connection failed. Check your network and try again."
        }

        if (failureReason != null) {
            // The MediaStore entry and its written bytes stay exactly as they
            // are -- the next attempt at this same unique work name resumes
            // from `statSizeOrZero(mediaUri)`, not from zero.
            return Result.retry()
        }

        markComplete(mediaUri)
        stateStore.clearDownload(workName)
        return Result.success(workDataOf(KEY_RESULT_PATH to mediaUri.toString()))
    }

    private fun resolveTargetUri(workName: String, filename: String, mimeType: String): Uri? {
        val resolver = applicationContext.contentResolver
        val existing = stateStore.downloadState(workName)?.mediaUri?.let(Uri::parse)
        if (existing != null) {
            if (uriIsUsable(existing)) return existing
            // A prior attempt's entry disappeared from under us (e.g. cleared
            // manually from the system Downloads app) -- start over with a
            // fresh entry rather than failing forever against a dead URI.
            stateStore.clearDownload(workName)
        }
        val values = ContentValues().apply {
            put(MediaStore.Downloads.DISPLAY_NAME, filename)
            put(MediaStore.Downloads.MIME_TYPE, mimeType)
            put(MediaStore.Downloads.IS_PENDING, 1)
        }
        val inserted = resolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values) ?: return null
        stateStore.saveDownloadUri(workName, inserted.toString())
        return inserted
    }

    private fun uriIsUsable(uri: Uri): Boolean = try {
        applicationContext.contentResolver.openFileDescriptor(uri, "r")?.use { true } ?: false
    } catch (e: FileNotFoundException) {
        false
    } catch (e: SecurityException) {
        false
    }

    private fun statSizeOrZero(uri: Uri): Long = try {
        applicationContext.contentResolver.openFileDescriptor(uri, "r")?.use { it.statSize } ?: 0L
    } catch (e: FileNotFoundException) {
        0L
    }

    private fun markComplete(uri: Uri) {
        val values = ContentValues().apply { put(MediaStore.Downloads.IS_PENDING, 0) }
        applicationContext.contentResolver.update(uri, values, null, null)
    }
}
