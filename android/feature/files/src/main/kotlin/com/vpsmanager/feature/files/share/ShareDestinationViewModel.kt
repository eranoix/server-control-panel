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

/** One item from the incoming share [android.content.Intent], with its display name and size. */
data class SharedItem(val uri: String, val displayName: String, val sizeBytes: Long)

/**
 * Renames repeated display names to `"name (2).ext"`, `"name (3).ext"` and so on. Duplicates are
 * common (items without `DISPLAY_NAME` share a fallback name), and would crash the keyed
 * `LazyColumn` and make `ExistingWorkPolicy.KEEP` silently drop uploads with the same work name.
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
 * Chooses the share destination: the default inbox or a folder picked in the embedded browser.
 * Uploading itself stays with [com.vpsmanager.feature.files.transfer.TransferViewModel].
 *
 * An `AndroidViewModel` because [uploadWorkInfo] needs a Context for [WorkManager.getInstance].
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
     * Observes the upload's unique work by name. Must match the `"upload:$destDir/$filename"`
     * convention of `TransferViewModel.startUpload`.
     */
    fun uploadWorkInfo(destDir: String, filename: String): Flow<WorkInfo?> =
        workManager.getWorkInfosForUniqueWorkFlow(uploadWorkName(destDir, filename)).map { it.firstOrNull() }
}

private fun uploadWorkName(destDir: String, filename: String) = "upload:$destDir/$filename"
