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
import com.vpsmanager.core.shell.shellInsertionText
import java.time.Instant
import java.util.UUID
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** How far along ONE attachment is. */
sealed interface AttachmentState {
    /** `percentual == [UNKNOWN_PERCENT]` when the provider did not report the size. */
    data class Uploading(val percent: Int) : AttachmentState

    /** Arrived. [path] is absolute on the server and can already be a command argument. */
    data class Ready(val path: String) : AttachmentState

    data class Failed(val reason: String) : AttachmentState

    data object Cancelled : AttachmentState
}

/** One row of the attachments bar. */
data class ScreenAttachment(
    val id: UUID,
    val name: String,
    val state: AttachmentState,
)

/**
 * Attachments as the terminal screen sees them: it enqueues one
 * [AttachmentUploadWorker] per chosen file, follows each of them, and keeps the
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
 * See [TerminalAttachmentViewModel.insertionText].
 *
 * `AndroidViewModel` because `WorkManager.getInstance` requires an application
 * `Context` — the same precedent `TransferViewModel` (`:feature-files`) has
 * already set, without introducing new dependency injection for this single
 * use.
 */
class TerminalAttachmentViewModel(application: Application) : AndroidViewModel(application) {

    private val workManager = WorkManager.getInstance(application)

    private val _attachments = MutableStateFlow<List<ScreenAttachment>>(emptyList())
    val attachments: StateFlow<List<ScreenAttachment>> = _attachments.asStateFlow()

    /**
     * The network is the only requirement. Without it WorkManager holds the
     * work in the queue rather than letting it fail — which is the right
     * behaviour for the context of use (a connection that comes and goes).
     */
    private val requiresNetwork = Constraints.Builder()
        .setRequiredNetworkType(NetworkType.CONNECTED)
        .build()

    /**
     * Enqueues one upload per item. Several at once work because each becomes
     * a unique work of its own, named after the destination name — which
     * already carries a timestamp and therefore never collides with another
     * attachment.
     */
    fun attach(items: List<LocalAttachment>, now: Instant = Instant.now()) {
        items.forEach { item ->
            val destinationName = destinationNameFor(item.displayName, now)
            val workName = "anexo:$destinationName"
            val request = OneTimeWorkRequestBuilder<AttachmentUploadWorker>()
                .setConstraints(requiresNetwork)
                .setInputData(
                    workDataOf(
                        KEY_WORK_NAME to workName,
                        KEY_SOURCE_URI to item.uri,
                        KEY_DESTINATION_NAME to destinationName,
                        KEY_TOTAL_BYTES to item.sizeBytes,
                    ),
                )
                .build()
            workManager.enqueueUniqueWork(workName, ExistingWorkPolicy.KEEP, request)
            _attachments.value = _attachments.value + ScreenAttachment(
                id = request.id,
                name = item.displayName,
                state = AttachmentState.Uploading(UNKNOWN_PERCENT),
            )
            observe(request.id)
        }
    }

    /** Cancels an upload in flight; whatever was already written on the server is swept by the 24h reaper. */
    fun cancel(id: UUID) {
        workManager.cancelWorkById(id)
    }

    /** Removes the row from the bar (an attachment already inserted, or an error already read). */
    fun discard(id: UUID) {
        workManager.cancelWorkById(id)
        _attachments.value = _attachments.value.filterNot { it.id == id }
    }

    /**
     * The exact text to paste into the command line for the attachments
     * already finished — each path quoted if it needs to be, separated by
     * spaces, ending in a space and NEVER in a line break (see
     * `shellInsertionText`). Empty when nothing is ready, in which case
     * the caller inserts nothing.
     */
    fun insertionText(ids: List<UUID>): String = insertionTextFrom(_attachments.value, ids)

    /** All the ones that have arrived, in the order they were attached. */
    fun readyIds(): List<UUID> = _attachments.value.filter { it.state is AttachmentState.Ready }.map { it.id }

    private fun observe(id: UUID) {
        viewModelScope.launch {
            workManager.getWorkInfoByIdFlow(id).collect { info ->
                if (info == null) return@collect
                _attachments.value = _attachments.value.map { attachment ->
                    if (attachment.id == id) attachment.copy(state = info.toState()) else attachment
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
internal fun insertionTextFrom(attachments: List<ScreenAttachment>, ids: List<UUID>): String {
    val paths = attachments
        .filter { it.id in ids }
        .mapNotNull { (it.state as? AttachmentState.Ready)?.path }
    return shellInsertionText(paths)
}

internal fun WorkInfo.toState(): AttachmentState = when (state) {
    WorkInfo.State.RUNNING, WorkInfo.State.ENQUEUED, WorkInfo.State.BLOCKED ->
        AttachmentState.Uploading(progress.getInt(KEY_PERCENT, UNKNOWN_PERCENT))
    WorkInfo.State.SUCCEEDED -> {
        val path = outputData.getString(KEY_RESULT_PATH)
        if (path.isNullOrBlank()) {
            AttachmentState.Failed("The server did not return the file path.")
        } else {
            AttachmentState.Ready(path)
        }
    }
    WorkInfo.State.FAILED -> AttachmentState.Failed(
        outputData.getString(KEY_ERROR_REASON) ?: "Could not upload the attachment.",
    )
    WorkInfo.State.CANCELLED -> AttachmentState.Cancelled
}
