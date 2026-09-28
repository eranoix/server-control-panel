package dev.servercontrolpanel.feature.files.browse

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.servercontrolpanel.core.model.FileEntry
import dev.servercontrolpanel.data.files.FileListResult
import dev.servercontrolpanel.data.files.FilesRepository
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

internal const val ROOT_PATH = "/"

sealed interface FileBrowserUiState {
    data object Loading : FileBrowserUiState
    data class Error(val message: String) : FileBrowserUiState
    data object Empty : FileBrowserUiState
    data class Success(val currentPath: String, val entries: List<FileEntry>) : FileBrowserUiState
}

class FileBrowserViewModel(
    private val filesRepository: FilesRepository = FilesRepository(),
) : ViewModel() {

    private val _uiState = MutableStateFlow<FileBrowserUiState>(FileBrowserUiState.Loading)
    val uiState: StateFlow<FileBrowserUiState> = _uiState.asStateFlow()

    var currentPath: String = ROOT_PATH
        private set

    init {
        load(ROOT_PATH)
    }

    fun navigateInto(entry: FileEntry) {
        if (!entry.isDir) return
        load(joinPath(currentPath, entry.name))
    }

    fun navigateUp() {
        if (currentPath == ROOT_PATH) return
        load(parentOf(currentPath))
    }

    fun retry() {
        load(currentPath)
    }

    fun goTo(path: String) {
        if (path == currentPath) return
        load(path)
    }

    fun pathFor(entry: FileEntry): String = joinPath(currentPath, entry.name)

    private fun load(path: String) {
        currentPath = path
        _uiState.value = FileBrowserUiState.Loading
        viewModelScope.launch {
            _uiState.value = when (val result = filesRepository.list(path)) {
                is FileListResult.Success -> FileBrowserUiState.Success(
                    currentPath = result.path,
                    entries = result.entries,
                )
                is FileListResult.Empty -> FileBrowserUiState.Empty
                is FileListResult.Error -> FileBrowserUiState.Error(result.reason)
            }
        }
    }
}

private fun joinPath(base: String, name: String): String =
    if (base.endsWith("/")) "$base$name" else "$base/$name"

private fun parentOf(path: String): String {
    val trimmed = path.trimEnd('/')
    if (trimmed.isEmpty()) return ROOT_PATH
    val separatorIndex = trimmed.lastIndexOf('/')
    return if (separatorIndex <= 0) ROOT_PATH else trimmed.substring(0, separatorIndex)
}
