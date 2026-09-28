package dev.servercontrolpanel.feature.files.transfer

import android.content.Context
import android.net.Uri
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import dev.servercontrolpanel.data.files.ChunkedUploadOutcome
import dev.servercontrolpanel.data.files.ChunkedUploadPump
import dev.servercontrolpanel.data.files.TransferRepository
import dev.servercontrolpanel.data.files.UploadByteSource
import dev.servercontrolpanel.data.files.UploadSessionResult

class UploadWorker @JvmOverloads constructor(
    context: Context,
    params: WorkerParameters,
    private val transferRepository: TransferRepository = TransferRepository(chunkStagingDir = context.cacheDir),
    private val stateStore: TransferStateStore = TransferStateStore(context),
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
                stateStore.saveUploadProgress(workName, outcome.bytesSent)
                Result.retry()
            }
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
