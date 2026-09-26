package com.vpsmanager.feature.files.editor

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.vpsmanager.data.files.FileReadResult
import com.vpsmanager.data.files.FileWriteResult
import com.vpsmanager.data.files.FilesRepository
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/**
 * State machine for the file editor screen (open + syntax highlight, save
 * with conflict detection). [Editing] is the only state
 * [com.vpsmanager.feature.files.editor.SoraEditorView] renders text into;
 * [Saving] carries the same fields so the screen keeps showing the buffer
 * (read-only) while a write is in flight instead of blanking it.
 *
 * [Conflict] is reached only from a [FileWriteResult.Conflict] -- the
 * mtime-based check the BFF performs on every write. It is never entered
 * automatically resolved: the admin must pick reload, overwrite or cancel.
 */
sealed interface FileEditorUiState {
    data object Loading : FileEditorUiState

    data class Error(val message: String) : FileEditorUiState

    data class Editing(
        val path: String,
        val content: String,
        val mtime: Long,
        val language: String,
        val isDirty: Boolean,
        /** Set when a save attempt failed for a reason other than a conflict; cleared on the next edit or load. */
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
        /** The mtime the rejected write was sent with -- restored verbatim by [resolveCancel]. */
        val localMtime: Long,
        val serverContent: String,
        val serverMtime: Long,
        val language: String,
    ) : FileEditorUiState
}

/**
 * Drives one file's open/edit/save/conflict-resolution lifecycle. One
 * instance per opened file (scoped to its own navigation back-stack entry,
 * see `FileEditorScreen`'s `viewModel()` factory) -- [path] is fixed for the
 * lifetime of the instance.
 */
class FileEditorViewModel(
    private val path: String,
    private val filesRepository: FilesRepository = FilesRepository(),
) : ViewModel() {

    private val _uiState = MutableStateFlow<FileEditorUiState>(FileEditorUiState.Loading)
    val uiState: StateFlow<FileEditorUiState> = _uiState.asStateFlow()

    init {
        load()
    }

    /** Re-reads the file from scratch, discarding any local edits -- used by the initial load and the error-screen retry action. */
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

    /** Buffers the editor's current text. Only marks the buffer dirty -- it never triggers a network call by itself. */
    fun onContentChanged(newContent: String) {
        val current = _uiState.value as? FileEditorUiState.Editing ?: return
        _uiState.value = current.copy(content = newContent, isDirty = true, saveError = null)
    }

    /** No-op unless the buffer actually has unsaved changes -- mirrors the screen's save button being disabled while `!isDirty`. */
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

    /** Discards the local edit entirely and re-enters [FileEditorUiState.Editing] with what's actually on disk. */
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

    /**
     * Explicit, user-confirmed override: re-issues the write with the
     * server's own mtime as the new `expected_mtime`, so this attempt
     * either succeeds cleanly against the version just shown to the admin,
     * or reports a fresh conflict if the file moved again in the meantime.
     */
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

    /**
     * Keeps the local, unsaved edit exactly as it was and returns to
     * [FileEditorUiState.Editing] -- neither the server's version nor the
     * local one is written. [localMtime] (the mtime this edit was based on,
     * now known stale) is restored unchanged so a later [save] still runs
     * the same conflict check instead of silently succeeding.
     */
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
