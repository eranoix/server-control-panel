package com.vpsmanager.feature.terminal.attach

import android.content.Context
import android.net.Uri
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import com.vpsmanager.data.files.ChunkedUploadOutcome
import com.vpsmanager.data.files.ChunkedUploadPump
import com.vpsmanager.data.files.FilesRepository
import com.vpsmanager.data.files.InboxDirResult
import com.vpsmanager.data.files.TransferRepository
import com.vpsmanager.data.files.UploadByteSource
import com.vpsmanager.data.files.UploadResumeStore
import com.vpsmanager.data.files.UploadSessionResult

/** Keys of the `Data` exchanged between [TerminalAttachmentViewModel] and this worker. */
internal const val KEY_WORK_NAME = "anexo_nome_do_trabalho"
internal const val KEY_SOURCE_URI = "anexo_uri_de_origem"
internal const val KEY_DESTINATION_NAME = "anexo_nome_de_destino"
internal const val KEY_TOTAL_BYTES = "anexo_total_bytes"
internal const val KEY_PERCENT = "anexo_percentual"
internal const val KEY_RESULT_PATH = "anexo_caminho_resultante"
internal const val KEY_ERROR_REASON = "anexo_motivo_do_erro"

/** `-1` = percentage unknown (the provider did not report the size). */
internal const val UNKNOWN_PERCENT = -1

internal fun percentOf(sent: Long, total: Long): Int {
    if (total <= 0) return UNKNOWN_PERCENT
    return ((sent * 100) / total).toInt().coerceIn(0, 100)
}

/**
 * Sends ONE attachment picked on the terminal screen to the server's inbox
 * folder, and returns the final absolute path.
 *
 * **Why a `Worker` and not a ViewModel coroutine.** The context of use is a bad
 * connection: the upload has to survive leaving the screen, rotating the
 * device, losing the network halfway through and the process being killed — and
 * resume from the byte the server acknowledged, not from zero. That is exactly
 * what WorkManager (with its network constraint and its retry policy) plus
 * [ChunkedUploadPump] deliver together; a ViewModel coroutine dies with the
 * screen.
 *
 * **Where the file lands.** In the directory the BFF itself already publishes
 * at `GET /files/inbox` (`<dataDir>/mobile-inbox`) — no new route was invented
 * for this. The directory is resolved HERE, on every attempt, rather than
 * captured on the screen: after a process death the worker has to know the
 * destination without depending on anything that only ever lived in memory.
 *
 * **No foreground notification, on purpose.** The progress of this upload is
 * watched inside the terminal screen (that is where the path is going to be
 * used), and the attachment bar shows the same state. A persistent notification
 * per attachment would be noise for an upload that lasts seconds.
 *
 * **`@JvmOverloads` is not ornamental — without it this worker DOES NOT RUN.**
 * WorkManager's default factory instantiates a worker by reflection, looking
 * for exactly the `(Context, WorkerParameters)` constructor. Parameters with
 * default values in Kotlin do not generate that constructor: they generate the
 * full one plus a synthetic one with a bitmask. Without the annotation the
 * factory throws `NoSuchMethodException`, WorkManager logs "Could not create
 * Worker" and marks the work as FAILED — the attachment fails BEFORE the first
 * line of [doWork], and the screen shows a generic error because not even the
 * reason got written. That is exactly what happened on the first run in the
 * emulator.
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
            // The server refuses total_size <= 0 with a 413 ("too large"), which
            // would be a baffling lie for an empty file. Better to tell the
            // truth before spending a request.
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
                    // `retryable` is what separates "the network dropped" (retry
                    // by itself) from "this file will never fit / there is no
                    // permission" (fail NOW, with the reason on screen).
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
                // The staging session stays on the server and will be swept by
                // the 24h reaper; clearing the local memory is what stops a
                // future resume from trying to continue a doomed session.
                resumeStore.clear(workName)
                failure(outcome.reason)
            }
        }
    }

    private fun failure(reason: String) = Result.failure(workDataOf(KEY_ERROR_REASON to reason))
}
