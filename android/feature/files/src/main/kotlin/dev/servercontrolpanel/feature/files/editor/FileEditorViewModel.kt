package dev.servercontrolpanel.feature.files.editor

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.servercontrolpanel.data.files.FileReadResult
import dev.servercontrolpanel.data.files.FileWriteResult
import dev.servercontrolpanel.data.files.FilesRepository
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

sealed interface FileEditorUiState {
    data object Loading : FileEditorUiState

    data class Error(val message: String) : FileEditorUiState

    data class Editing(
        val path: String,
        val content: String,
        val mtime: Long,
        val language: String,
        val isDirty: Boolean,
        val saveError: String? = null,
    ) : FileEditorUiState

    data class Saving(
        val path: String,
        val content: String,
        val mtime: Long,
        val language: String,
    ) : FileEditorUiState

    data class Conflict(
        val path: String,
        val localContent: String,
        val localMtime: Long,
        val serverContent: String,
        val serverMtime: Long,
        val language: String,
    ) : FileEditorUiState
}

class FileEditorViewModel(
    private val path: String,
    private val filesRepository: FilesRepository = FilesRepository(),
) : ViewModel() {

    private val _uiState = MutableStateFlow<FileEditorUiState>(FileEditorUiState.Loading)
    val uiState: StateFlow<FileEditorUiState> = _uiState.asStateFlow()

    init {
        load()
    }

    fun retry() = load()

    private fun load() {
        _uiState.value = FileEditorUiState.Loading
        viewModelScope.launch {
            _uiState.value = when (val result = filesRepository.read(path)) {
                is FileReadResult.Success -> FileEditorUiState.Editing(
                    path = path,
                    content = result.content,
                    mtime = result.mtime,
                    language = result.language,
                    isDirty = false,
                )
                is FileReadResult.Error -> FileEditorUiState.Error(result.reason)
            }
        }
    }

    fun onContentChanged(newContent: String) {
        val current = _uiState.value as? FileEditorUiState.Editing ?: return
        _uiState.value = current.copy(content = newContent, isDirty = true, saveError = null)
    }

    fun save() {
        val current = _uiState.value as? FileEditorUiState.Editing ?: return
        if (!current.isDirty) return
        _uiState.value = FileEditorUiState.Saving(
            path = current.path,
            content = current.content,
            mtime = current.mtime,
            language = current.language,
        )
        viewModelScope.launch {
            val result = filesRepository.write(current.path, current.content, current.mtime)
            applyWriteResult(
                result = result,
                path = current.path,
                content = current.content,
                language = current.language,
                attemptedMtime = current.mtime,
            )
        }
    }

    fun resolveReload() {
        val current = _uiState.value as? FileEditorUiState.Conflict ?: return
        _uiState.value = FileEditorUiState.Editing(
            path = current.path,
            content = current.serverContent,
            mtime = current.serverMtime,
            language = current.language,
            isDirty = false,
        )
    }

    fun resolveOverwrite() {
        val current = _uiState.value as? FileEditorUiState.Conflict ?: return
        _uiState.value = FileEditorUiState.Saving(
            path = current.path,
            content = current.localContent,
            mtime = current.serverMtime,
            language = current.language,
        )
        viewModelScope.launch {
            val result = filesRepository.write(current.path, current.localContent, current.serverMtime)
            applyWriteResult(
                result = result,
                path = current.path,
                content = current.localContent,
                language = current.language,
                attemptedMtime = current.serverMtime,
            )
        }
    }

    fun resolveCancel() {
        val current = _uiState.value as? FileEditorUiState.Conflict ?: return
        _uiState.value = FileEditorUiState.Editing(
            path = current.path,
            content = current.localContent,
            mtime = current.localMtime,
            language = current.language,
            isDirty = true,
        )
    }

    private fun applyWriteResult(
        result: FileWriteResult,
        path: String,
        content: String,
        language: String,
        attemptedMtime: Long,
    ) {
        _uiState.value = when (result) {
            is FileWriteResult.Success -> FileEditorUiState.Editing(
                path = path,
                content = content,
                mtime = result.mtime,
                language = language,
                isDirty = false,
            )
            is FileWriteResult.Conflict -> FileEditorUiState.Conflict(
                path = path,
                localContent = content,
                localMtime = attemptedMtime,
                serverContent = result.serverContent,
                serverMtime = result.serverMtime,
                language = language,
            )
            is FileWriteResult.Error -> FileEditorUiState.Editing(
                path = path,
                content = content,
                mtime = attemptedMtime,
                language = language,
                isDirty = true,
                saveError = result.reason,
            )
        }
    }
}
