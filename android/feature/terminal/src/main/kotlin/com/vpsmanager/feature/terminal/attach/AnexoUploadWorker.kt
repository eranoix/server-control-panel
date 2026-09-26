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

/** Keys of the `Data` exchanged between [TerminalAnexoViewModel] and this worker. */
internal const val CHAVE_NOME_DO_TRABALHO = "anexo_nome_do_trabalho"
internal const val CHAVE_URI_DE_ORIGEM = "anexo_uri_de_origem"
internal const val CHAVE_NOME_DE_DESTINO = "anexo_nome_de_destino"
internal const val CHAVE_TOTAL_BYTES = "anexo_total_bytes"
internal const val CHAVE_PERCENTUAL = "anexo_percentual"
internal const val CHAVE_CAMINHO_RESULTANTE = "anexo_caminho_resultante"
internal const val CHAVE_MOTIVO_DO_ERRO = "anexo_motivo_do_erro"

/** `-1` = percentage unknown (the provider did not report the size). */
internal const val PERCENTUAL_DESCONHECIDO = -1

internal fun percentualDe(enviados: Long, total: Long): Int {
    if (total <= 0) return PERCENTUAL_DESCONHECIDO
    return ((enviados * 100) / total).toInt().coerceIn(0, 100)
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
class AnexoUploadWorker @JvmOverloads constructor(
    context: Context,
    params: WorkerParameters,
    private val transferRepository: TransferRepository = TransferRepository(chunkStagingDir = context.cacheDir),
    private val filesRepository: FilesRepository = FilesRepository(),
    private val resumeStore: UploadResumeStore = UploadResumeStore(context),
    private val pump: ChunkedUploadPump = ChunkedUploadPump(transferRepository),
) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result {
        val nomeDoTrabalho = inputData.getString(CHAVE_NOME_DO_TRABALHO) ?: return falha("Invalid attachment.")
        val uriDeOrigem = inputData.getString(CHAVE_URI_DE_ORIGEM)?.let(Uri::parse)
            ?: return falha("Attachment has no source.")
        val nomeDeDestino = inputData.getString(CHAVE_NOME_DE_DESTINO) ?: return falha("Attachment has no name.")
        val totalBytes = inputData.getLong(CHAVE_TOTAL_BYTES, 0L)

        if (totalBytes <= 0L) {
            // The server refuses total_size <= 0 with a 413 ("too large"), which
            // would be a baffling lie for an empty file. Better to tell the
            // truth before spending a request.
            return falha("The file is empty — there is nothing to upload.")
        }

        val sessaoExistente = resumeStore.state(nomeDoTrabalho)
        val sessao = sessaoExistente ?: when (val destino = filesRepository.inboxPath()) {
            is InboxDirResult.Error -> return Result.retry()
            is InboxDirResult.Success -> {
                when (val iniciada = transferRepository.startUpload(destino.path, nomeDeDestino, totalBytes)) {
                    is UploadSessionResult.Started -> {
                        resumeStore.saveSession(nomeDoTrabalho, iniciada.sessionId)
                        resumeStore.state(nomeDoTrabalho)
                    }
                    // `retryable` is what separates "the network dropped" (retry
                    // by itself) from "this file will never fit / there is no
                    // permission" (fail NOW, with the reason on screen).
                    is UploadSessionResult.Error ->
                        return if (iniciada.retryable) Result.retry() else falha(iniciada.reason)
                }
            }
        } ?: return Result.retry()

        val desfecho = pump.send(
            source = UploadByteSource { applicationContext.contentResolver.openInputStream(uriDeOrigem) },
            sessionId = sessao.sessionId,
            startOffset = sessao.bytesUploaded,
            totalSize = totalBytes,
            onProgress = { enviados ->
                resumeStore.saveProgress(nomeDoTrabalho, enviados)
                setProgress(workDataOf(CHAVE_PERCENTUAL to percentualDe(enviados, totalBytes)))
            },
        )

        return when (desfecho) {
            is ChunkedUploadOutcome.Completed -> {
                resumeStore.clear(nomeDoTrabalho)
                Result.success(workDataOf(CHAVE_CAMINHO_RESULTANTE to desfecho.path))
            }
            is ChunkedUploadOutcome.Interrupted -> {
                resumeStore.saveProgress(nomeDoTrabalho, desfecho.bytesSent)
                Result.retry()
            }
            is ChunkedUploadOutcome.Refused -> {
                // The staging session stays on the server and will be swept by
                // the 24h reaper; clearing the local memory is what stops a
                // future resume from trying to continue a doomed session.
                resumeStore.clear(nomeDoTrabalho)
                falha(desfecho.reason)
            }
        }
    }

    private fun falha(motivo: String) = Result.failure(workDataOf(CHAVE_MOTIVO_DO_ERRO to motivo))
}
