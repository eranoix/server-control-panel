package com.vpsmanager.feature.files.transfer

import android.app.Application
import android.net.Uri
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import androidx.work.Constraints
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkInfo
import androidx.work.WorkManager
import androidx.work.workDataOf
import java.util.UUID
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** Rendered state of one in-flight (or finished) transfer, keyed by its WorkManager id. */
sealed interface TransferUiState {
    data class InProgress(val percent: Int) : TransferUiState
    data class Completed(val path: String) : TransferUiState
    data class Failed(val reason: String) : TransferUiState
    data object Cancelled : TransferUiState
}

/**
 * Enqueues and observes [DownloadWorker]/[UploadWorker] jobs. `AndroidViewModel`
 * (not a plain `ViewModel` with a manually-wired `Context`) because
 * `WorkManager.getInstance` needs an application `Context` and this is the
 * standard, DI-free way to get one inside a `ViewModel` -- no new wiring
 * pattern introduced for this single need.
 */
class TransferViewModel(application: Application) : AndroidViewModel(application) {

    private val workManager = WorkManager.getInstance(application)
    private val stateStore = TransferStateStore(application)
    // Deferred cleanup: cancelling a worker throws CancellationException out
    // of doWork() before DownloadWorker/UploadWorker's own terminal-state
    // cleanup runs, so cleanup on CANCELLED is done here instead (see observe).
    private val garbageCollector = TransferGarbageCollector(stateStore)
    private val _transfers = MutableStateFlow<Map<UUID, TransferUiState>>(emptyMap())
    val transfers: StateFlow<Map<UUID, TransferUiState>> = _transfers.asStateFlow()

    /** Only a transfer requires network -- WorkManager holds the job until connectivity returns. */
    private val transferConstraints = Constraints.Builder()
        .setRequiredNetworkType(NetworkType.CONNECTED)
        .build()

    fun startDownload(path: String, filename: String, mimeType: String, totalSize: Long) {
        val workName = "download:$path"
        val request = OneTimeWorkRequestBuilder<DownloadWorker>()
            .setConstraints(transferConstraints)
            .setInputData(
                workDataOf(
                    KEY_WORK_NAME to workName,
                    KEY_SERVER_PATH to path,
                    KEY_FILENAME to filename,
                    KEY_MIME_TYPE to mimeType,
                    KEY_TOTAL_BYTES to totalSize,
                ),
            )
            .build()
        // KEEP: re-triggering a download that is still enqueued/running must
        // not stack a duplicate job -- once the existing job reaches a terminal
        // state (cancelled/failed/succeeded), the next trigger enqueues fresh
        // and resumes from the persisted state regardless.
        workManager.enqueueUniqueWork(workName, ExistingWorkPolicy.KEEP, request)
        observe(request.id, workName)
    }

    fun startUpload(sourceUri: String, destDir: String, filename: String, totalSize: Long) {
        val workName = "upload:$destDir/$filename"
        val request = OneTimeWorkRequestBuilder<UploadWorker>()
            .setConstraints(transferConstraints)
            .setInputData(
                workDataOf(
                    KEY_WORK_NAME to workName,
                    KEY_SOURCE_URI to sourceUri,
                    KEY_DEST_DIR to destDir,
                    KEY_FILENAME to filename,
                    KEY_TOTAL_BYTES to totalSize,
                ),
            )
            .build()
        workManager.enqueueUniqueWork(workName, ExistingWorkPolicy.KEEP, request)
        observe(request.id, workName)
    }

    fun cancel(workId: UUID) {
        workManager.cancelWorkById(workId)
    }

    private fun observe(workId: UUID, workName: String) {
        viewModelScope.launch {
            workManager.getWorkInfoByIdFlow(workId).collect { info ->
                if (info == null) return@collect
                _transfers.value = _transfers.value + (workId to info.toUiState())
                if (info.state == WorkInfo.State.CANCELLED) {
                    // By the time WorkManager reports CANCELLED, the worker has
                    // fully stopped -- safe to delete the MediaStore row and
                    // clear the resume state without racing a live writer.
                    garbageCollector.cleanupCancelled(workName) { mediaUri ->
                        getApplication<Application>().contentResolver.delete(Uri.parse(mediaUri), null, null)
                    }
                }
            }
        }
    }
}

private fun WorkInfo.toUiState(): TransferUiState = when (state) {
    WorkInfo.State.RUNNING, WorkInfo.State.ENQUEUED, WorkInfo.State.BLOCKED ->
        TransferUiState.InProgress(progress.getInt(KEY_PERCENT, PERCENT_UNKNOWN))
    WorkInfo.State.SUCCEEDED -> TransferUiState.Completed(outputData.getString(KEY_RESULT_PATH) ?: "")
    WorkInfo.State.FAILED -> TransferUiState.Failed(
        outputData.getString(KEY_ERROR_REASON) ?: "Could not finish the transfer.",
    )
    WorkInfo.State.CANCELLED -> TransferUiState.Cancelled
}
