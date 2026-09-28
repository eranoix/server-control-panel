package dev.servercontrolpanel.feature.terminal.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.servercontrolpanel.data.terminal.ActionResult
import dev.servercontrolpanel.data.terminal.TargetsResult
import dev.servercontrolpanel.data.terminal.BackupsResult
import dev.servercontrolpanel.data.terminal.PreviewResult
import dev.servercontrolpanel.data.terminal.SessionBackup
import dev.servercontrolpanel.data.terminal.TerminalBackupSource
import dev.servercontrolpanel.data.terminal.TerminalRepository
import dev.servercontrolpanel.data.terminal.TerminalSession
import dev.servercontrolpanel.data.terminal.TerminalSessionsResult
import dev.servercontrolpanel.data.terminal.TerminalSessionsSource
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

sealed interface SessionListUiState {
    data object Loading : SessionListUiState
    data class Error(val message: String) : SessionListUiState
    data object Empty : SessionListUiState
    data class Success(val sessions: List<TerminalSession>) : SessionListUiState
}

sealed interface BackupsUiState {
    data object Idle : BackupsUiState
    data object Loading : BackupsUiState
    data object Empty : BackupsUiState
    data class Ready(val backups: List<SessionBackup>) : BackupsUiState
    data class Error(val message: String) : BackupsUiState
}

sealed interface PreviewUiState {
    data object Loading : PreviewUiState
    data object Empty : PreviewUiState
    data class Ready(val text: String) : PreviewUiState
    data class Error(val message: String) : PreviewUiState
}

class SessionListViewModel(
    private val sessionsSource: TerminalSessionsSource = TerminalRepository(),
    private val backupSource: TerminalBackupSource = sessionsSource as? TerminalBackupSource
        ?: TerminalRepository(),
) : ViewModel() {

    private val _uiState = MutableStateFlow<SessionListUiState>(SessionListUiState.Loading)
    val uiState: StateFlow<SessionListUiState> = _uiState.asStateFlow()

    private val _backups = MutableStateFlow<BackupsUiState>(BackupsUiState.Idle)
    val backups: StateFlow<BackupsUiState> = _backups.asStateFlow()

    private val _notice = MutableStateFlow<String?>(null)
    val notice: StateFlow<String?> = _notice.asStateFlow()

    private val _busySession = MutableStateFlow<String?>(null)
    val busySession: StateFlow<String?> = _busySession.asStateFlow()

    private val _targets = MutableStateFlow<List<String>?>(null)
    val targets: StateFlow<List<String>?> = _targets.asStateFlow()

    private val _previews = MutableStateFlow<Map<String, PreviewUiState>>(emptyMap())
    val previews: StateFlow<Map<String, PreviewUiState>> = _previews.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _uiState.value = SessionListUiState.Loading
        viewModelScope.launch {
            _uiState.value = when (val result = sessionsSource.sessions()) {
                is TerminalSessionsResult.Success -> SessionListUiState.Success(result.sessions)
                is TerminalSessionsResult.Empty -> SessionListUiState.Empty
                is TerminalSessionsResult.Error -> SessionListUiState.Error(result.reason)
            }
        }
    }

    fun loadBackups() {
        _backups.value = BackupsUiState.Loading
        viewModelScope.launch {
            _backups.value = when (val r = backupSource.backups()) {
                is BackupsResult.Success -> BackupsUiState.Ready(r.backups)
                is BackupsResult.Empty -> BackupsUiState.Empty
                is BackupsResult.Error -> BackupsUiState.Error(r.reason)
            }
        }
    }

    fun saveBackup(session: String? = null) = run(session) {
        backupSource.createBackup(session)
    }

    fun restore(id: String, session: String? = null) = run(session, reloadList = true) {
        backupSource.restore(id, session)
    }

    fun deleteBackup(id: String, session: String? = null) = run(session) {
        backupSource.deleteBackup(id, session)
    }

    fun rename(from: String, to: String) = run(from, reloadList = true, reloadBackups = false) {
        backupSource.renameSession(from, to)
    }

    fun kill(name: String) = run(name, reloadList = true, reloadBackups = false) {
        backupSource.killSession(name)
    }

    fun assign(name: String, target: String) = run(name, reloadList = true, reloadBackups = false) {
        backupSource.assignSession(name, target)
    }

    fun loadTargets() {
        if (_targets.value != null) return
        viewModelScope.launch {
            _targets.value = when (val r = backupSource.assignmentTargets()) {
                is TargetsResult.Success -> r.targets
                is TargetsResult.Error -> emptyList()
            }
        }
    }

    fun togglePreview(name: String) {
        val openPreviews = _previews.value
        if (openPreviews.containsKey(name)) {
            _previews.value = openPreviews - name
            return
        }
        _previews.value = openPreviews + (name to PreviewUiState.Loading)
        viewModelScope.launch {
            val state = when (val r = backupSource.sessionPreview(name)) {
                is PreviewResult.Success ->
                    if (r.text.isBlank()) PreviewUiState.Empty
                    else PreviewUiState.Ready(r.text)
                is PreviewResult.Error -> PreviewUiState.Error(r.reason)
            }
            if (_previews.value.containsKey(name)) {
                _previews.value = _previews.value + (name to state)
            }
        }
    }

    fun clearNotice() {
        _notice.value = null
    }

    private fun run(
        session: String?,
        reloadList: Boolean = false,
        reloadBackups: Boolean = true,
        tile: suspend () -> ActionResult,
    ) {
        _busySession.value = session ?: ALL
        viewModelScope.launch {
            val r = tile()
            _busySession.value = null
            _notice.value = when (r) {
                is ActionResult.Ok -> r.message
                is ActionResult.Error -> r.reason
                is ActionResult.Queued -> r.message
            }
            if (r is ActionResult.Ok) {
                if (reloadList) refresh()
                if (reloadBackups && _backups.value != BackupsUiState.Idle) loadBackups()
            }
        }
    }

    private fun <T> MutableStateFlow<T>.clear(value: T) = update { value }

    companion object {
        const val ALL: String = "*"
    }
}
