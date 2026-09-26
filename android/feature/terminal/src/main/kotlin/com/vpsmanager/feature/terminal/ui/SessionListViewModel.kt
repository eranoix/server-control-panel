package com.vpsmanager.feature.terminal.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.vpsmanager.data.terminal.ActionResult
import com.vpsmanager.data.terminal.TargetsResult
import com.vpsmanager.data.terminal.BackupsResult
import com.vpsmanager.data.terminal.PreviewResult
import com.vpsmanager.data.terminal.SessionBackup
import com.vpsmanager.data.terminal.TerminalBackupSource
import com.vpsmanager.data.terminal.TerminalRepository
import com.vpsmanager.data.terminal.TerminalSession
import com.vpsmanager.data.terminal.TerminalSessionsResult
import com.vpsmanager.data.terminal.TerminalSessionsSource
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/**
 * State of the session list. It distinguishes "there are no sessions yet"
 * ([Empty]) from a failure ([Error]) so the screen never has to guess — the
 * same shape used by the app's other screens.
 */
sealed interface SessionListUiState {
    data object Loading : SessionListUiState
    data class Error(val message: String) : SessionListUiState
    data object Empty : SessionListUiState
    data class Success(val sessions: List<TerminalSession>) : SessionListUiState
}

/** State of the backups sheet, loaded only when it opens. */
sealed interface BackupsUiState {
    data object Idle : BackupsUiState
    data object Loading : BackupsUiState
    data object Empty : BackupsUiState
    data class Ready(val backups: List<SessionBackup>) : BackupsUiState
    data class Error(val message: String) : BackupsUiState
}

/** State of ONE session's preview. It exists only while the preview is open. */
sealed interface PreviewUiState {
    data object Loading : PreviewUiState
    data object Empty : PreviewUiState
    data class Ready(val text: String) : PreviewUiState
    data class Error(val message: String) : PreviewUiState
}

/**
 * Drives the sessions screen: the list, the backups and the operations that
 * change something.
 *
 * ## Why backups and list are separate states
 * The list is what the screen always shows; the backups only matter once the
 * sheet opens, and loading them alongside would cost a read of every one of the
 * user's backup files each time the screen appears. [loadBackups] is called
 * when the sheet opens, not in `init`.
 *
 * ## Why [notice] exists
 * Every operation here is a write and happens away from its result: whoever
 * taps "restore" is looking at the sheet, and what changes is the list behind
 * it. Without a sentence reporting back, the action looks as though it never
 * happened — and the person taps again.
 */
class SessionListViewModel(
    private val sessionsSource: TerminalSessionsSource = TerminalRepository(),
    private val backupSource: TerminalBackupSource = sessionsSource as? TerminalBackupSource
        ?: TerminalRepository(),
) : ViewModel() {

    private val _uiState = MutableStateFlow<SessionListUiState>(SessionListUiState.Loading)
    val uiState: StateFlow<SessionListUiState> = _uiState.asStateFlow()

    private val _backups = MutableStateFlow<BackupsUiState>(BackupsUiState.Idle)
    val backups: StateFlow<BackupsUiState> = _backups.asStateFlow()

    /** The last sentence reporting back on an operation. The screen consumes it and clears it. */
    private val _notice = MutableStateFlow<String?>(null)
    val notice: StateFlow<String?> = _notice.asStateFlow()

    /** Name of the session with an operation in flight, so the row can show progress. */
    private val _busySession = MutableStateFlow<String?>(null)
    val busySession: StateFlow<String?> = _busySession.asStateFlow()

    /** Assignment targets. `null` = we have not asked yet. */
    private val _targets = MutableStateFlow<List<String>?>(null)
    val targets: StateFlow<List<String>?> = _targets.asStateFlow()

    /** Open previews, by session name. Absent = closed. */
    private val _previews = MutableStateFlow<Map<String, PreviewUiState>>(emptyMap())
    val previews: StateFlow<Map<String, PreviewUiState>> = _previews.asStateFlow()

    init {
        refresh()
        // loadTargets() does NOT belong here. Constructing a ViewModel must
        // not fire off every call it will ever make: the screen asks for the
        // targets when it appears, so whoever constructs it controls what
        // starts. It was putting this in init that revealed the leak between
        // tests — a coroutine still pending at teardown blows up in the next
        // test.
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

    /** A null [session] saves every session in a single bundle. */
    fun saveBackup(session: String? = null) = run(session) {
        backupSource.createBackup(session)
    }

    /**
     * Restoring touches the session list (it creates the missing ones), so it
     * reloads both things: the list, which is what the person is going to use,
     * and the backups, which is what they are looking at.
     */
    fun restore(id: String, session: String? = null) = run(session, reloadList = true) {
        backupSource.restore(id, session)
    }

    fun deleteBackup(id: String, session: String? = null) = run(session) {
        backupSource.deleteBackup(id, session)
    }

    fun rename(from: String, to: String) = run(from, reloadList = true, reloadBackups = false) {
        backupSource.renameSession(from, to)
    }

    /**
     * Kills the session. The caller has ALREADY confirmed — this method does
     * not ask.
     *
     * The confirmation lives in the screen because that is where the name the
     * person is about to lose exists; only the string would reach this far.
     */
    fun kill(name: String) = run(name, reloadList = true, reloadBackups = false) {
        backupSource.killSession(name)
    }

    fun assign(name: String, target: String) = run(name, reloadList = true, reloadBackups = false) {
        backupSource.assignSession(name, target)
    }

    /**
     * Loads the possible targets, once only.
     *
     * The route is admin-only and returns 404 to everyone else — which is the
     * same as saying "this feature does not exist for you". Storing the empty
     * list in that case is the right thing: the option disappears from the menu
     * instead of opening an empty sheet.
     */
    fun loadTargets() {
        if (_targets.value != null) return
        viewModelScope.launch {
            _targets.value = when (val r = backupSource.assignmentTargets()) {
                is TargetsResult.Success -> r.targets
                is TargetsResult.Error -> emptyList()
            }
        }
    }

    /**
     * Opens or closes a session's preview.
     *
     * Closing DISCARDS the text rather than keeping it: the preview is a
     * portrait of one instant, and reopening it showing the old portrait would
     * assert as current something that has aged — the same reason the app's
     * offline cache carries an age strip.
     */
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
            // If the person closed it while it was loading, do not reopen it under them.
            if (_previews.value.containsKey(name)) {
                _previews.value = _previews.value + (name to state)
            }
        }
    }

    fun clearNotice() {
        _notice.value = null
    }

    /**
     * The shared body of every write operation: mark busy, run it, store the
     * sentence reporting back and reload whatever the operation may have
     * changed.
     *
     * Reloading only on success is deliberate: after an error the list on
     * screen is still the truth, and reloading it would flash the whole screen
     * to show exactly the same content.
     */
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
                // Once queued it is a message, not a reload: the server does
                // not know anything yet, and reloading the list would show the
                // OLD state right after saying the action had been stored —
                // making it look as though it had failed.
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
        /** Marker for "an operation over every session", for [busySession]. */
        const val ALL: String = "*"
    }
}
