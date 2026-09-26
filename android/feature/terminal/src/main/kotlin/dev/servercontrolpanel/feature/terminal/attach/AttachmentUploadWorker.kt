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

/** Keys of the `Data` exchanged between [TerminalAttachmentViewModel] and this worker. */
internal const val KEY_WORK_NAME = "attachment_work_name"
internal const val KEY_SOURCE_URI = "attachment_source_uri"
internal const val KEY_DESTINATION_NAME = "attachment_destination_name"
internal const val KEY_TOTAL_BYTES = "attachment_total_bytes"
internal const val KEY_PERCENT = "attachment_percent"
internal const val KEY_RESULT_PATH = "attachment_result_path"
internal const val KEY_ERROR_REASON = "attachment_error_reason"

/** `-1` = unknown percentage (the provider did not report the size). */
internal const val UNKNOWN_PERCENT = -1

internal fun percentOf(sent: Long, total: Long): Int {
    if (total <= 0) return UNKNOWN_PERCENT
    return ((sent * 100) / total).toInt().coerceIn(0, 100)
}

/**
 * Sends ONE attachment picked on the terminal screen to the server's inbox folder
 * and returns the final absolute path.
 *
 * A `Worker` so the upload survives leaving the screen, network loss and process
 * death, resuming from the byte the server acknowledged ([ChunkedUploadPump]).
 * The destination is the BFF's `GET /files/inbox` directory, resolved on every
 * attempt so nothing depends on in-memory state. No foreground notification: the
 * attachment bar already shows progress for an upload that lasts seconds.
 *
 * `@JvmOverloads` is required: WorkManager's default factory looks up the
 * `(Context, WorkerParameters)` constructor by reflection, and Kotlin default
 * parameters do not generate it, so the work would fail before [doWork] runs.
 */
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
            // The server rejects total_size <= 0 with a misleading 413, so say
            // it plainly before spending a request.
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
                    // `retryable` separates a dropped network (retry) from a
                    // permanent refusal (fail now, with the reason on screen).
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
                // The server's 24h reaper removes the staging session; clearing
                // local state stops a future resume of a doomed session.
                resumeStore.clear(workName)
                failure(outcome.reason)
            }
        }
    }

    private fun failure(reason: String) = Result.failure(workDataOf(KEY_ERROR_REASON to reason))
}
