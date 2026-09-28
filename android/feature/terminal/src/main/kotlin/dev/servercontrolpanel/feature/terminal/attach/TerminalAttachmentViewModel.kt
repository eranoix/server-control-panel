package dev.servercontrolpanel.feature.terminal.attach

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
import dev.servercontrolpanel.core.shell.shellInsertionText
import java.time.Instant
import java.util.UUID
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

sealed interface AttachmentState {
    data class Uploading(val percent: Int) : AttachmentState

    data class Ready(val path: String) : AttachmentState

    data class Failed(val reason: String) : AttachmentState

    data object Cancelled : AttachmentState
}

data class ScreenAttachment(
    val id: UUID,
    val name: String,
    val state: AttachmentState,
)

class TerminalAttachmentViewModel(application: Application) : AndroidViewModel(application) {

    private val workManager = WorkManager.getInstance(application)

    private val _attachments = MutableStateFlow<List<ScreenAttachment>>(emptyList())
    val attachments: StateFlow<List<ScreenAttachment>> = _attachments.asStateFlow()

    private val requiresNetwork = Constraints.Builder()
        .setRequiredNetworkType(NetworkType.CONNECTED)
        .build()

    fun attach(items: List<LocalAttachment>, now: Instant = Instant.now()) {
        items.forEach { item ->
            val destinationName = destinationNameFor(item.displayName, now)
            val workName = "attachment:$destinationName"
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

    fun cancel(id: UUID) {
        workManager.cancelWorkById(id)
    }

    fun discard(id: UUID) {
        workManager.cancelWorkById(id)
        _attachments.value = _attachments.value.filterNot { it.id == id }
    }

    fun insertionText(ids: List<UUID>): String = insertionTextFrom(_attachments.value, ids)

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
