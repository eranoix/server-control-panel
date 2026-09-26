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

/** How far along one attachment is. */
sealed interface AttachmentState {
    /** `percent == [UNKNOWN_PERCENT]` when the provider did not report the size. */
    data class Uploading(val percent: Int) : AttachmentState

    /** Arrived. [path] is absolute on the server and ready to use as a command argument. */
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
 * Terminal screen attachments: enqueues one [AttachmentUploadWorker] per file,
 * follows each, and keeps the returned path until the operator inserts it.
 *
 * It never types into the terminal on its own: an upload may finish while the
 * user is mid-command or inside `vim`, so a finished attachment waits with an
 * insert button (see [TerminalAttachmentViewModel.insertionText]).
 *
 * `AndroidViewModel` because `WorkManager.getInstance` needs the application
 * `Context` (same approach as `TransferViewModel` in `:feature-files`).
 */
class TerminalAttachmentViewModel(application: Application) : AndroidViewModel(application) {

    private val workManager = WorkManager.getInstance(application)

    private val _attachments = MutableStateFlow<List<ScreenAttachment>>(emptyList())
    val attachments: StateFlow<List<ScreenAttachment>> = _attachments.asStateFlow()

    /** Network is the only constraint; without it WorkManager keeps the work queued instead of failing it. */
    private val requiresNetwork = Constraints.Builder()
        .setRequiredNetworkType(NetworkType.CONNECTED)
        .build()

    /**
     * Enqueues one upload per item, each as unique work named after its
     * timestamped destination name, so they never collide.
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

    /** Cancels an upload in flight; partial server data is swept by the 24h reaper. */
    fun cancel(id: UUID) {
        workManager.cancelWorkById(id)
    }

    /** Removes the row from the bar (an inserted attachment or an error already read). */
    fun discard(id: UUID) {
        workManager.cancelWorkById(id)
        _attachments.value = _attachments.value.filterNot { it.id == id }
    }

    /**
     * The exact text to paste into the command line for finished attachments:
     * shell-quoted paths separated by spaces, ending in a space and never a line
     * break (see `shellInsertionText`). Empty when nothing is ready.
     */
    fun insertionText(ids: List<UUID>): String = insertionTextFrom(_attachments.value, ids)

    /** All finished attachments, in the order they were attached. */
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
 * The insertion rule, kept pure so it can be tested without WorkManager: only
 * finished attachments, in attach order, each path quoted for the shell.
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
