package com.vpsmanager.feature.files.browse

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.vpsmanager.core.model.FileEntry
import com.vpsmanager.data.files.FileListResult
import com.vpsmanager.data.files.FilesRepository
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/**
 * Starting directory for the browser. `validatePath` on the BFF already
 * denies the genuinely sensitive subtrees, so the root of the filesystem is
 * a simple, defensible default rather than a locked product decision --
 * changing it later is a one-line edit here, no architectural rework.
 */
internal const val ROOT_PATH = "/"

/**
 * State rendered by [com.vpsmanager.feature.files.browse.FileBrowserScreen].
 * Distinguishes "directory has zero entries" ([Empty]) from a hard failure
 * ([Error]) so the UI never has to guess.
 */
sealed interface FileBrowserUiState {
    data object Loading : FileBrowserUiState
    data class Error(val message: String) : FileBrowserUiState
    data object Empty : FileBrowserUiState
    data class Success(val currentPath: String, val entries: List<FileEntry>) : FileBrowserUiState
}

/**
 * Drives the file browser vertical slice. The only data source is
 * [FilesRepository.list], itself the single call site into the generated
 * mobile BFF client. Every navigation event (entering a directory,
 * navigating up, retrying) re-fetches through the repository -- there is no
 * client-side directory cache to go stale.
 */
class FileBrowserViewModel(
    private val filesRepository: FilesRepository = FilesRepository(),
) : ViewModel() {

    private val _uiState = MutableStateFlow<FileBrowserUiState>(FileBrowserUiState.Loading)
    val uiState: StateFlow<FileBrowserUiState> = _uiState.asStateFlow()

    /** The directory currently requested -- read by the screen to decide whether "up" applies. */
    var currentPath: String = ROOT_PATH
        private set

    init {
        load(ROOT_PATH)
    }

    /** Only meaningful for a directory entry; a no-op for files (the screen routes those elsewhere). */
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

    /**
     * Goes straight to [caminho] — the jump the header's breadcrumb makes.
     *
     * It exists apart from [navigateInto] because the breadcrumb does not
     * navigate to a CHILD: it jumps to an arbitrary ANCESTOR, possibly several
     * levels up. Doing that with repeated [navigateUp] would load one
     * intermediate folder per level — four requests and four repaints to reach
     * a place that was already known.
     *
     * It ignores the current path: a tap on the step you are already on is the
     * only way to reload by accident, and the breadcrumb already disables that
     * step.
     */
    fun irPara(caminho: String) {
        if (caminho == currentPath) return
        load(caminho)
    }

    /**
     * [FileEntry] carries no path of its own (only name/size/isDir) -- the
     * screen needs the full path to hand off to the file editor, so this
     * reuses the same join logic [navigateInto] already applies for
     * directories.
     */
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

/** Mirrors the server's `filepath.Dir` semantics closely enough for "go up one level". */
private fun parentOf(path: String): String {
    val trimmed = path.trimEnd('/')
    if (trimmed.isEmpty()) return ROOT_PATH
    val separatorIndex = trimmed.lastIndexOf('/')
    return if (separatorIndex <= 0) ROOT_PATH else trimmed.substring(0, separatorIndex)
}
