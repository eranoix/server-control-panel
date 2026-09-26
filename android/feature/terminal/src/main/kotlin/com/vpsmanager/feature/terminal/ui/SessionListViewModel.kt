package com.vpsmanager.feature.terminal.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.vpsmanager.data.terminal.AcaoResult
import com.vpsmanager.data.terminal.AlvosResult
import com.vpsmanager.data.terminal.BackupsResult
import com.vpsmanager.data.terminal.PreviaResult
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
    data object Ocioso : BackupsUiState
    data object Carregando : BackupsUiState
    data object Vazio : BackupsUiState
    data class Pronto(val backups: List<SessionBackup>) : BackupsUiState
    data class Erro(val mensagem: String) : BackupsUiState
}

/** State of ONE session's preview. It exists only while the preview is open. */
sealed interface PreviaUiState {
    data object Carregando : PreviaUiState
    data object Vazia : PreviaUiState
    data class Pronta(val texto: String) : PreviaUiState
    data class Erro(val mensagem: String) : PreviaUiState
}

/**
 * Drives the sessions screen: the list, the backups and the operations that
 * change something.
 *
 * ## Why backups and list are separate states
 * The list is what the screen always shows; the backups only matter once the
 * sheet opens, and loading them alongside would cost a read of every one of the
 * user's backup files each time the screen appears. [carregarBackups] is called
 * when the sheet opens, not in `init`.
 *
 * ## Why [recado] exists
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

    private val _backups = MutableStateFlow<BackupsUiState>(BackupsUiState.Ocioso)
    val backups: StateFlow<BackupsUiState> = _backups.asStateFlow()

    /** The last sentence reporting back on an operation. The screen consumes it and clears it. */
    private val _recado = MutableStateFlow<String?>(null)
    val recado: StateFlow<String?> = _recado.asStateFlow()

    /** Name of the session with an operation in flight, so the row can show progress. */
    private val _ocupada = MutableStateFlow<String?>(null)
    val ocupada: StateFlow<String?> = _ocupada.asStateFlow()

    /** Assignment targets. `null` = we have not asked yet. */
    private val _alvos = MutableStateFlow<List<String>?>(null)
    val alvos: StateFlow<List<String>?> = _alvos.asStateFlow()

    /** Open previews, by session name. Absent = closed. */
    private val _previas = MutableStateFlow<Map<String, PreviaUiState>>(emptyMap())
    val previas: StateFlow<Map<String, PreviaUiState>> = _previas.asStateFlow()

    init {
        refresh()
        // carregarAlvos() does NOT belong here. Constructing a ViewModel must
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

    fun carregarBackups() {
        _backups.value = BackupsUiState.Carregando
        viewModelScope.launch {
            _backups.value = when (val r = backupSource.backups()) {
                is BackupsResult.Success -> BackupsUiState.Pronto(r.backups)
                is BackupsResult.Empty -> BackupsUiState.Vazio
                is BackupsResult.Error -> BackupsUiState.Erro(r.reason)
            }
        }
    }

    /** A null [sessao] saves every session in a single bundle. */
    fun salvarBackup(sessao: String? = null) = executar(sessao) {
        backupSource.criarBackup(sessao)
    }

    /**
     * Restoring touches the session list (it creates the missing ones), so it
     * reloads both things: the list, which is what the person is going to use,
     * and the backups, which is what they are looking at.
     */
    fun restaurar(id: String, sessao: String? = null) = executar(sessao, recarregaLista = true) {
        backupSource.restaurar(id, sessao)
    }

    fun excluirBackup(id: String, sessao: String? = null) = executar(sessao) {
        backupSource.excluirBackup(id, sessao)
    }

    fun renomear(de: String, para: String) = executar(de, recarregaLista = true, recarregaBackups = false) {
        backupSource.renomearSessao(de, para)
    }

    /**
     * Kills the session. The caller has ALREADY confirmed — this method does
     * not ask.
     *
     * The confirmation lives in the screen because that is where the name the
     * person is about to lose exists; only the string would reach this far.
     */
    fun matar(nome: String) = executar(nome, recarregaLista = true, recarregaBackups = false) {
        backupSource.matarSessao(nome)
    }

    fun atribuir(nome: String, alvo: String) = executar(nome, recarregaLista = true, recarregaBackups = false) {
        backupSource.atribuirSessao(nome, alvo)
    }

    /**
     * Loads the possible targets, once only.
     *
     * The route is admin-only and returns 404 to everyone else — which is the
     * same as saying "this feature does not exist for you". Storing the empty
     * list in that case is the right thing: the option disappears from the menu
     * instead of opening an empty sheet.
     */
    fun carregarAlvos() {
        if (_alvos.value != null) return
        viewModelScope.launch {
            _alvos.value = when (val r = backupSource.alvosDeAtribuicao()) {
                is AlvosResult.Success -> r.alvos
                is AlvosResult.Error -> emptyList()
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
    fun alternarPrevia(nome: String) {
        val abertas = _previas.value
        if (abertas.containsKey(nome)) {
            _previas.value = abertas - nome
            return
        }
        _previas.value = abertas + (nome to PreviaUiState.Carregando)
        viewModelScope.launch {
            val estado = when (val r = backupSource.previaDaSessao(nome)) {
                is PreviaResult.Success ->
                    if (r.texto.isBlank()) PreviaUiState.Vazia
                    else PreviaUiState.Pronta(r.texto)
                is PreviaResult.Error -> PreviaUiState.Erro(r.reason)
            }
            // If the person closed it while it was loading, do not reopen it under them.
            if (_previas.value.containsKey(nome)) {
                _previas.value = _previas.value + (nome to estado)
            }
        }
    }

    fun limparRecado() {
        _recado.value = null
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
    private fun executar(
        sessao: String?,
        recarregaLista: Boolean = false,
        recarregaBackups: Boolean = true,
        bloco: suspend () -> AcaoResult,
    ) {
        _ocupada.value = sessao ?: TODAS
        viewModelScope.launch {
            val r = bloco()
            _ocupada.value = null
            _recado.value = when (r) {
                is AcaoResult.Ok -> r.mensagem
                is AcaoResult.Erro -> r.reason
                // Once queued it is a message, not a reload: the server does
                // not know anything yet, and reloading the list would show the
                // OLD state right after saying the action had been stored —
                // making it look as though it had failed.
                is AcaoResult.NaFila -> r.mensagem
            }
            if (r is AcaoResult.Ok) {
                if (recarregaLista) refresh()
                if (recarregaBackups && _backups.value != BackupsUiState.Ocioso) carregarBackups()
            }
        }
    }

    private fun <T> MutableStateFlow<T>.limpar(valor: T) = update { valor }

    companion object {
        /** Marker for "an operation over every session", for [ocupada]. */
        const val TODAS: String = "*"
    }
}
