package com.vpsmanager.feature.terminal.attach

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import androidx.work.Constraints
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkInfo
import androidx.work.WorkManager
import androidx.work.workDataOf
import com.vpsmanager.core.shell.textoDeInsercaoParaShell
import java.time.Instant
import java.util.UUID
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** How far along ONE attachment is. */
sealed interface EstadoDoAnexo {
    /** `percentual == [PERCENTUAL_DESCONHECIDO]` when the provider did not report the size. */
    data class Enviando(val percentual: Int) : EstadoDoAnexo

    /** Arrived. [caminho] is absolute on the server and can already be a command argument. */
    data class Pronto(val caminho: String) : EstadoDoAnexo

    data class Falhou(val motivo: String) : EstadoDoAnexo

    data object Cancelado : EstadoDoAnexo
}

/** One row of the attachments bar. */
data class AnexoNaTela(
    val id: UUID,
    val nome: String,
    val estado: EstadoDoAnexo,
)

/**
 * Attachments as the terminal screen sees them: it enqueues one
 * [AnexoUploadWorker] per chosen file, follows each of them, and keeps the
 * path that came back from the server until the operator decides to insert it
 * into the command line.
 *
 * **What this object deliberately does NOT do: type into the terminal by
 * itself.** When an upload finishes, whoever is on the other end may be
 * halfway through a command, inside `vim`, or in a full-screen program reading
 * key by key. Injecting text there, unasked, at the moment the network
 * happened to finish, would corrupt what the person was writing — and the
 * moment would be unpredictable precisely because it depends on the network.
 * So a finished attachment STAYS VISIBLE with its path and an insert button:
 * the insertion happens when the operator says it is time, in a single tap.
 * See [TerminalAnexoViewModel.textoParaInserir].
 *
 * `AndroidViewModel` because `WorkManager.getInstance` requires an application
 * `Context` — the same precedent `TransferViewModel` (`:feature-files`) has
 * already set, without introducing new dependency injection for this single
 * use.
 */
class TerminalAnexoViewModel(application: Application) : AndroidViewModel(application) {

    private val workManager = WorkManager.getInstance(application)

    private val _anexos = MutableStateFlow<List<AnexoNaTela>>(emptyList())
    val anexos: StateFlow<List<AnexoNaTela>> = _anexos.asStateFlow()

    /**
     * The network is the only requirement. Without it WorkManager holds the
     * work in the queue rather than letting it fail — which is the right
     * behaviour for the context of use (a connection that comes and goes).
     */
    private val exigeRede = Constraints.Builder()
        .setRequiredNetworkType(NetworkType.CONNECTED)
        .build()

    /**
     * Enqueues one upload per item. Several at once work because each becomes
     * a unique work of its own, named after the destination name — which
     * already carries a timestamp and therefore never collides with another
     * attachment.
     */
    fun anexar(itens: List<AnexoLocal>, agora: Instant = Instant.now()) {
        itens.forEach { item ->
            val nomeDeDestino = nomeDeDestinoPara(item.nomeExibido, agora)
            val nomeDoTrabalho = "anexo:$nomeDeDestino"
            val pedido = OneTimeWorkRequestBuilder<AnexoUploadWorker>()
                .setConstraints(exigeRede)
                .setInputData(
                    workDataOf(
                        CHAVE_NOME_DO_TRABALHO to nomeDoTrabalho,
                        CHAVE_URI_DE_ORIGEM to item.uri,
                        CHAVE_NOME_DE_DESTINO to nomeDeDestino,
                        CHAVE_TOTAL_BYTES to item.tamanhoBytes,
                    ),
                )
                .build()
            workManager.enqueueUniqueWork(nomeDoTrabalho, ExistingWorkPolicy.KEEP, pedido)
            _anexos.value = _anexos.value + AnexoNaTela(
                id = pedido.id,
                nome = item.nomeExibido,
                estado = EstadoDoAnexo.Enviando(PERCENTUAL_DESCONHECIDO),
            )
            observar(pedido.id)
        }
    }

    /** Cancels an upload in flight; whatever was already written on the server is swept by the 24h reaper. */
    fun cancelar(id: UUID) {
        workManager.cancelWorkById(id)
    }

    /** Removes the row from the bar (an attachment already inserted, or an error already read). */
    fun descartar(id: UUID) {
        workManager.cancelWorkById(id)
        _anexos.value = _anexos.value.filterNot { it.id == id }
    }

    /**
     * The exact text to paste into the command line for the attachments
     * already finished — each path quoted if it needs to be, separated by
     * spaces, ending in a space and NEVER in a line break (see
     * `textoDeInsercaoParaShell`). Empty when nothing is ready, in which case
     * the caller inserts nothing.
     */
    fun textoParaInserir(ids: List<UUID>): String = textoParaInserirDe(_anexos.value, ids)

    /** All the ones that have arrived, in the order they were attached. */
    fun idsProntos(): List<UUID> = _anexos.value.filter { it.estado is EstadoDoAnexo.Pronto }.map { it.id }

    private fun observar(id: UUID) {
        viewModelScope.launch {
            workManager.getWorkInfoByIdFlow(id).collect { info ->
                if (info == null) return@collect
                _anexos.value = _anexos.value.map { anexo ->
                    if (anexo.id == id) anexo.copy(estado = info.paraEstado()) else anexo
                }
            }
        }
    }
}

/**
 * The insertion rule, pure and outside the ViewModel so it can be exercised
 * without WorkManager: only attachments that are ALREADY FINISHED go in (an
 * upload in flight has no path to insert, and inserting the path of one that
 * failed would point at a file that does not exist), in the order they were
 * attached, each path quoted for the shell.
 */
internal fun textoParaInserirDe(anexos: List<AnexoNaTela>, ids: List<UUID>): String {
    val caminhos = anexos
        .filter { it.id in ids }
        .mapNotNull { (it.estado as? EstadoDoAnexo.Pronto)?.caminho }
    return textoDeInsercaoParaShell(caminhos)
}

internal fun WorkInfo.paraEstado(): EstadoDoAnexo = when (state) {
    WorkInfo.State.RUNNING, WorkInfo.State.ENQUEUED, WorkInfo.State.BLOCKED ->
        EstadoDoAnexo.Enviando(progress.getInt(CHAVE_PERCENTUAL, PERCENTUAL_DESCONHECIDO))
    WorkInfo.State.SUCCEEDED -> {
        val caminho = outputData.getString(CHAVE_CAMINHO_RESULTANTE)
        if (caminho.isNullOrBlank()) {
            EstadoDoAnexo.Falhou("The server did not return the file path.")
        } else {
            EstadoDoAnexo.Pronto(caminho)
        }
    }
    WorkInfo.State.FAILED -> EstadoDoAnexo.Falhou(
        outputData.getString(CHAVE_MOTIVO_DO_ERRO) ?: "Could not upload the attachment.",
    )
    WorkInfo.State.CANCELLED -> EstadoDoAnexo.Cancelado
}
