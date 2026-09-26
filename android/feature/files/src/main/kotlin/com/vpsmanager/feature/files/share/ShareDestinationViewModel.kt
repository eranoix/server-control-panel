package com.vpsmanager.feature.files.share

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import androidx.work.WorkInfo
import androidx.work.WorkManager
import com.vpsmanager.data.files.FilesRepository
import com.vpsmanager.data.files.InboxDirResult
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch

/**
 * One file/text item extracted from the incoming share [android.content.Intent]
 * by `ShareTargetActivity`, already resolved to a display name and size so
 * [ShareDestinationScreen] can show what is being shared before committing to
 * an upload.
 */
data class SharedItem(val uri: String, val displayName: String, val sizeBytes: Long)

/**
 * Renames every [SharedItem] after the first to carry a given `displayName`
 * within [items] to `"name (2).ext"`, `"name (3).ext"`, etc. -- an
 * `ACTION_SEND_MULTIPLE` share of two items whose source app didn't expose a
 * `DISPLAY_NAME` column (both fall back to the literal `"arquivo"`, see
 * `ShareTargetActivity.resolveSharedUri`), or two photos that simply share a
 * filename, is an ordinary occurrence -- not an edge case. Without this,
 * downstream consumers that key by `displayName` collide: `ShareDestinationScreen`'s
 * `LazyColumn` throws (Compose requires unique keys), and
 * `TransferViewModel.startUpload`'s `WorkManager` unique-work name
 * (`"upload:$destDir/$filename"`) silently drops every item after the first
 * to the same name via `ExistingWorkPolicy.KEEP`. Called once, at the point
 * [SharedItem]s are first handed to the UI, so every consumer downstream
 * (the list, the upload call, the per-item work-info query) agrees on the
 * same already-unique name.
 */
fun disambiguateSharedItems(items: List<SharedItem>): List<SharedItem> {
    val seenCounts = HashMap<String, Int>()
    return items.map { item ->
        val occurrence = (seenCounts[item.displayName] ?: 0) + 1
        seenCounts[item.displayName] = occurrence
        if (occurrence == 1) item else item.copy(displayName = suffixedName(item.displayName, occurrence))
    }
}

private fun suffixedName(displayName: String, occurrence: Int): String {
    val dotIndex = displayName.lastIndexOf('.')
    return if (dotIndex > 0) {
        "${displayName.substring(0, dotIndex)} ($occurrence)${displayName.substring(dotIndex)}"
    } else {
        "$displayName ($occurrence)"
    }
}

/** Which step of the share flow [ShareDestinationScreen] currently renders. */
sealed interface DestinationStep {
    data object ChooseDestination : DestinationStep
    data object PickFolder : DestinationStep
    data class Uploading(val destDir: String) : DestinationStep
}

/**
 * Drives the destination choice for a share: resolve the default inbox
 * directory, or let the admin pick a folder via [ShareDestinationScreen]'s
 * embedded `FileBrowserScreen(pickMode = true)`. This class never talks to
 * [com.vpsmanager.feature.files.transfer.UploadWorker]/`TransferRepository`
 * itself -- that stays exclusively [com.vpsmanager.feature.files.transfer.TransferViewModel]'s
 * job (the same engine built for the manual "Enviar" affordance), this class
 * only decides which `destDir` that engine is pointed at.
 *
 * `AndroidViewModel` (not a plain `ViewModel`) because [uploadWorkInfo] needs
 * an application `Context` to reach [WorkManager.getInstance] -- the same
 * precedent `TransferViewModel` already established, no new DI wiring
 * introduced for this single need.
 */
class ShareDestinationViewModel(
    application: Application,
    private val filesRepository: FilesRepository = FilesRepository(),
) : AndroidViewModel(application) {

    private val workManager = WorkManager.getInstance(application)

    private val _step = MutableStateFlow<DestinationStep>(DestinationStep.ChooseDestination)
    val step: StateFlow<DestinationStep> = _step.asStateFlow()

    private val _inboxError = MutableStateFlow<String?>(null)
    val inboxError: StateFlow<String?> = _inboxError.asStateFlow()

    fun choosePickFolder() {
        _inboxError.value = null
        _step.value = DestinationStep.PickFolder
    }

    fun cancelPickFolder() {
        _step.value = DestinationStep.ChooseDestination
    }

    /** Resolves the default inbox directory, then moves straight to uploading. */
    fun sendToInbox() {
        viewModelScope.launch {
            when (val result = filesRepository.inboxPath()) {
                is InboxDirResult.Success -> _step.value = DestinationStep.Uploading(result.path)
                is InboxDirResult.Error -> _inboxError.value = result.reason
            }
        }
    }

    /** A folder was picked via the embedded `FileBrowserScreen(pickMode = true)`. */
    fun folderPicked(destDir: String) {
        _step.value = DestinationStep.Uploading(destDir)
    }

    /**
     * Observes the same [WorkManager] unique-work entry
     * `TransferViewModel.startUpload` enqueues for `(destDir, filename)`,
     * queried directly by name rather than through `TransferViewModel`'s own
     * `StateFlow` (which is keyed by an internally-generated work id this
     * class never sees). This mirrors `"upload:$destDir/$filename"` --
     * `TransferViewModel.startUpload`'s own unique-work-name convention --
     * so this plan never has to touch a file under `feature/files/transfer/`
     * to correlate per-item progress.
     */
    fun uploadWorkInfo(destDir: String, filename: String): Flow<WorkInfo?> =
        workManager.getWorkInfosForUniqueWorkFlow(uploadWorkName(destDir, filename)).map { it.firstOrNull() }
}

private fun uploadWorkName(destDir: String, filename: String) = "upload:$destDir/$filename"
