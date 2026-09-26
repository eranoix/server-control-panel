package com.vpsmanager.feature.files.transfer

import android.content.Context
import android.net.Uri
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import com.vpsmanager.data.files.ChunkedUploadOutcome
import com.vpsmanager.data.files.ChunkedUploadPump
import com.vpsmanager.data.files.TransferRepository
import com.vpsmanager.data.files.UploadByteSource
import com.vpsmanager.data.files.UploadSessionResult

/**
 * Uploads a phone-local file (picked in
 * [com.vpsmanager.feature.files.browse.FileBrowserScreen] via
 * `ActivityResultContracts.OpenDocument`) to a server directory
 * through the init/chunk/complete session protocol, resuming from the last
 * chunk the server actually acknowledged after a cancellation, retry or
 * process death.
 *
 * **`@JvmOverloads` is mandatory here, not style.** WorkManager's default
 * factory instantiates by reflection looking for the exact
 * `(Context, WorkerParameters)` constructor, and Kotlin parameters with default
 * values do not generate that constructor — they generate the full one plus a
 * synthetic one with a bitmask. Without the annotation, WorkManager logs
 * "Could not create Worker" and marks the work FAILED before the first line of
 * [doWork]: the upload never happened on the device, and the screen only showed
 * a generic error (the reason never even got written down). Found by running a
 * terminal attachment on the emulator; this worker and [DownloadWorker] had the
 * same latent defect.
 */
class UploadWorker @JvmOverloads constructor(
    context: Context,
    params: WorkerParameters,
    private val transferRepository: TransferRepository = TransferRepository(chunkStagingDir = context.cacheDir),
    private val stateStore: TransferStateStore = TransferStateStore(context),
    // The chunk loop lives in :data ever since the terminal attachment came to
    // need the SAME loop (see ChunkedUploadPump). This worker was left with only
    // what is its own: the persisted session, the notification and WorkManager's
    // retry policy.
    private val pump: ChunkedUploadPump = ChunkedUploadPump(transferRepository),
) : CoroutineWorker(context, params) {

    private data class Session(val sessionId: String, val bytesUploaded: Long)

    override suspend fun doWork(): Result {
        val workName = inputData.getString(KEY_WORK_NAME) ?: return Result.failure()
        val sourceUri = inputData.getString(KEY_SOURCE_URI)?.let(Uri::parse) ?: return Result.failure()
        val destDir = inputData.getString(KEY_DEST_DIR) ?: return Result.failure()
        val filename = inputData.getString(KEY_FILENAME) ?: return Result.failure()
        val totalBytes = inputData.getLong(KEY_TOTAL_BYTES, 0L)

        val session = resolveSession(workName, destDir, filename, totalBytes) ?: return Result.retry()

        setForeground(
            TransferNotifications.foregroundInfo(
                context = applicationContext,
                workId = id,
                title = "Uploading $filename",
                percent = percentOf(session.bytesUploaded, totalBytes),
            ),
        )

        val outcome = pump.send(
            source = UploadByteSource { applicationContext.contentResolver.openInputStream(sourceUri) },
            sessionId = session.sessionId,
            startOffset = session.bytesUploaded,
            totalSize = totalBytes,
            onProgress = { sent ->
                stateStore.saveUploadProgress(workName, sent)
                setProgress(workDataOf(KEY_PERCENT to percentOf(sent, totalBytes)))
            },
        )

        return when (outcome) {
            is ChunkedUploadOutcome.Completed -> {
                stateStore.clearUpload(workName)
                Result.success(workDataOf(KEY_RESULT_PATH to outcome.path))
            }
            is ChunkedUploadOutcome.Interrupted -> {
                // Authoritative count straight from the server, always
                // trusted over our own local tally -- the same self-healing
                // principle DownloadWorker applies via MediaStore's file size.
                stateStore.saveUploadProgress(workName, outcome.bytesSent)
                Result.retry()
            }
            // Retrying does not help (file too large, disk full, no
            // permission): failing with the reason is what makes the screen tell
            // the truth instead of spinning forever.
            is ChunkedUploadOutcome.Refused -> Result.failure(workDataOf(KEY_ERROR_REASON to outcome.reason))
        }
    }

    private suspend fun resolveSession(
        workName: String,
        destDir: String,
        filename: String,
        totalBytes: Long,
    ): Session? {
        stateStore.uploadState(workName)?.let { return Session(it.sessionId, it.bytesUploaded) }
        return when (val started = transferRepository.startUpload(destDir, filename, totalBytes)) {
            is UploadSessionResult.Started -> {
                stateStore.saveUploadSession(workName, started.sessionId)
                Session(started.sessionId, 0L)
            }
            is UploadSessionResult.Error -> null
        }
    }

}
