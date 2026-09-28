package dev.servercontrolpanel.feature.files.share

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import androidx.work.WorkInfo
import androidx.work.WorkManager
import dev.servercontrolpanel.data.files.FilesRepository
import dev.servercontrolpanel.data.files.InboxDirResult
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch

data class SharedItem(val uri: String, val displayName: String, val sizeBytes: Long)

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

sealed interface DestinationStep {
    data object ChooseDestination : DestinationStep
    data object PickFolder : DestinationStep
    data class Uploading(val destDir: String) : DestinationStep
}

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

    fun sendToInbox() {
        viewModelScope.launch {
            when (val result = filesRepository.inboxPath()) {
                is InboxDirResult.Success -> _step.value = DestinationStep.Uploading(result.path)
                is InboxDirResult.Error -> _inboxError.value = result.reason
            }
        }
    }

    fun folderPicked(destDir: String) {
        _step.value = DestinationStep.Uploading(destDir)
    }

    fun uploadWorkInfo(destDir: String, filename: String): Flow<WorkInfo?> =
        workManager.getWorkInfosForUniqueWorkFlow(uploadWorkName(destDir, filename)).map { it.firstOrNull() }
}

private fun uploadWorkName(destDir: String, filename: String) = "upload:$destDir/$filename"
