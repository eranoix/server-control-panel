package com.vpsmanager.data.terminal

/**
 * One session inside a backup, already summarised by the server.
 *
 * The [resumo] is a single line saying what that session was about — it arrives
 * ready-made from the server because the one who can extract meaning from a
 * `capture-pane` is the one holding the scrollback. Without it, the list of
 * backups would be a column of timestamps, and choosing which to restore would
 * become guesswork.
 */
data class BackupSession(val nome: String, val resumo: String, val linhas: Int)

/**
 * A backup of terminal sessions.
 *
 * @property origem `manual` (a button), `auto` (the periodic collector) or
 *   `scheduled`. It appears on screen because it changes what the person
 *   expects: an automatic backup disappears by itself during pruning, a manual
 *   one is theirs.
 * @property bytes size on disk. A backup of 7 sessions with long histories goes
 *   past 10 MB, and knowing that is what prevents the surprise of a full disk.
 */
data class SessionBackup(
    val id: String,
    val criadoEm: Long,
    val origem: String?,
    val bytes: Long,
    val sessoes: List<BackupSession>,
)

/** The result of listing the backups. */
sealed interface BackupsResult {
    data class Success(val backups: List<SessionBackup>) : BackupsResult
    data object Empty : BackupsResult
    data class Error(val reason: String) : BackupsResult
}

/**
 * The result of an operation that CHANGES something (create, restore, delete,
 * rename).
 *
 * It returns a ready-made sentence rather than a boolean because every one of
 * these operations has a possible partial result — restoring 3 of 5 sessions is
 * the common case, not the exception — and "succeeded: yes/no" would erase
 * exactly the part the person needs to read.
 */
sealed interface AcaoResult {
    data class Ok(val mensagem: String) : AcaoResult
    data class Erro(val reason: String) : AcaoResult

    /**
     * The action never reached the server for lack of network and was STORED:
     * it goes out by itself when the internet is back.
     *
     * A state of its own, and not a gentler [Erro] or an optimistic [Ok] — for
     * the same reason `NA_FILA` exists in the WhatsApp send. An error would
     * make the person repeat the action (creating the second copy the queue
     * exists to prevent); an "ok" would lie about a backup that does not exist
     * yet.
     */
    data class NaFila(val mensagem: String) : AcaoResult
}

/**
 * The slice of [TerminalRepository] the sessions screen uses for backup and
 * management. Same discipline as [TerminalSessionsSource]: `:feature-terminal`
 * cannot see the generated client, so its tests fake this interface
 * and not `MobileApi`.
 */
interface TerminalBackupSource {
    suspend fun backups(): BackupsResult

    /** A null [sessao] saves ALL visible sessions in one package. */
    suspend fun criarBackup(sessao: String? = null): AcaoResult

    /** A null [sessao] restores every session in the backup. */
    suspend fun restaurar(id: String, sessao: String? = null): AcaoResult

    /** A null [sessao] deletes the whole backup; given one, only that session within it. */
    suspend fun excluirBackup(id: String, sessao: String? = null): AcaoResult

    suspend fun renomearSessao(de: String, para: String): AcaoResult

    /**
     * Kills the session and everything running inside it.
     *
     * Irreversible by design and with no safety net on the server side — it is
     * the caller that has to confirm first.
     */
    suspend fun matarSessao(nome: String): AcaoResult

    /**
     * Changes WHO the session appears to in the day-to-day list. [alvo] is a
     * username, or `"*"` for everyone. Admin only.
     */
    suspend fun atribuirSessao(nome: String, alvo: String): AcaoResult

    /**
     * The session's last lines, as plain text, without attaching to it.
     *
     * It is what answers "what is going on in there?" without the cost of
     * opening: opening an agent session changes what it shows, and on a phone
     * opening to look and going back is expensive.
     */
    suspend fun previaDaSessao(nome: String, linhas: Int = 20): PreviaResult

    /**
     * Who a session CAN be assigned to.
     *
     * It exists so the screen can OFFER the list. A free-text field would fail
     * silently: the server accepts any target, and a username with one
     * character wrong makes the session disappear from everybody's list, with
     * no error.
     */
    suspend fun alvosDeAtribuicao(): AlvosResult
}

/** Resultado de [TerminalBackupSource.alvosDeAtribuicao]. */
sealed interface AlvosResult {
    data class Success(val alvos: List<String>) : AlvosResult
    data class Error(val reason: String) : AlvosResult
}

/** Resultado de [TerminalBackupSource.previaDaSessao]. */
sealed interface PreviaResult {
    data class Success(val texto: String, val linhas: Int) : PreviaResult
    data class Error(val reason: String) : PreviaResult
}
