package dev.servercontrolpanel.feature.terminal.attach

import android.content.Context
import android.net.Uri
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import dev.servercontrolpanel.data.files.ChunkedUploadOutcome
import dev.servercontrolpanel.data.files.ChunkedUploadPump
import dev.servercontrolpanel.data.files.FilesRepository
import dev.servercontrolpanel.data.files.InboxDirResult
import dev.servercontrolpanel.data.files.TransferRepository
import dev.servercontrolpanel.data.files.UploadByteSource
import dev.servercontrolpanel.data.files.UploadResumeStore
import dev.servercontrolpanel.data.files.UploadSessionResult

internal const val KEY_WORK_NAME = "attachment_work_name"
internal const val KEY_SOURCE_URI = "attachment_source_uri"
internal const val KEY_DESTINATION_NAME = "attachment_destination_name"
internal const val KEY_TOTAL_BYTES = "attachment_total_bytes"
internal const val KEY_PERCENT = "attachment_percent"
internal const val KEY_RESULT_PATH = "attachment_result_path"
internal const val KEY_ERROR_REASON = "attachment_error_reason"

internal const val UNKNOWN_PERCENT = -1

internal fun percentOf(sent: Long, total: Long): Int {
    if (total <= 0) return UNKNOWN_PERCENT
    return ((sent * 100) / total).toInt().coerceIn(0, 100)
}

class AttachmentUploadWorker @JvmOverloads constructor(
    context: Context,
    params: WorkerParameters,
    private val transferRepository: TransferRepository = TransferRepository(chunkStagingDir = context.cacheDir),
    private val filesRepository: FilesRepository = FilesRepository(),
    private val resumeStore: UploadResumeStore = UploadResumeStore(context),
    private val pump: ChunkedUploadPump = ChunkedUploadPump(transferRepository),
) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result {
        val workName = inputData.getString(KEY_WORK_NAME) ?: return failure("Invalid attachment.")
        val sourceUri = inputData.getString(KEY_SOURCE_URI)?.let(Uri::parse)
            ?: return failure("Attachment has no source.")
        val destinationName = inputData.getString(KEY_DESTINATION_NAME) ?: return failure("Attachment has no name.")
        val totalBytes = inputData.getLong(KEY_TOTAL_BYTES, 0L)

        if (totalBytes <= 0L) {
            return failure("The file is empty — there is nothing to upload.")
        }

        val existingSession = resumeStore.state(workName)
        val session = existingSession ?: when (val destination = filesRepository.inboxPath()) {
            is InboxDirResult.Error -> return Result.retry()
            is InboxDirResult.Success -> {
                when (val started = transferRepository.startUpload(destination.path, destinationName, totalBytes)) {
                    is UploadSessionResult.Started -> {
                        resumeStore.saveSession(workName, started.sessionId)
                        resumeStore.state(workName)
                    }
                    is UploadSessionResult.Error ->
                        return if (started.retryable) Result.retry() else failure(started.reason)
                }
            }
        } ?: return Result.retry()

        val outcome = pump.send(
            source = UploadByteSource { applicationContext.contentResolver.openInputStream(sourceUri) },
            sessionId = session.sessionId,
            startOffset = session.bytesUploaded,
            totalSize = totalBytes,
            onProgress = { sent ->
                resumeStore.saveProgress(workName, sent)
                setProgress(workDataOf(KEY_PERCENT to percentOf(sent, totalBytes)))
            },
        )

        return when (outcome) {
            is ChunkedUploadOutcome.Completed -> {
                resumeStore.clear(workName)
                Result.success(workDataOf(KEY_RESULT_PATH to outcome.path))
            }
            is ChunkedUploadOutcome.Interrupted -> {
                resumeStore.saveProgress(workName, outcome.bytesSent)
                Result.retry()
            }
            is ChunkedUploadOutcome.Refused -> {
                resumeStore.clear(workName)
                failure(outcome.reason)
            }
        }
    }

    private fun failure(reason: String) = Result.failure(workDataOf(KEY_ERROR_REASON to reason))
}
