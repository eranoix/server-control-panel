package com.vpsmanager.data.terminal

/**
 * One session inside a backup, already summarised by the server.
 *
 * The [summary] is a single line saying what that session was about — it arrives
 * ready-made from the server because the one who can extract meaning from a
 * `capture-pane` is the one holding the scrollback. Without it, the list of
 * backups would be a column of timestamps, and choosing which to restore would
 * become guesswork.
 */
data class BackupSession(val name: String, val summary: String, val lines: Int)

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
    val createdAt: Long,
    val origin: String?,
    val bytes: Long,
    val sessions: List<BackupSession>,
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
sealed interface ActionResult {
    data class Ok(val message: String) : ActionResult
    data class Error(val reason: String) : ActionResult

    /**
     * The action never reached the server for lack of network and was STORED:
     * it goes out by itself when the internet is back.
     *
     * A state of its own, and not a gentler [Error] or an optimistic [Ok] — for
     * the same reason `QUEUED` exists in the WhatsApp send. An error would
     * make the person repeat the action (creating the second copy the queue
     * exists to prevent); an "ok" would lie about a backup that does not exist
     * yet.
     */
    data class Queued(val message: String) : ActionResult
}

/**
 * The slice of [TerminalRepository] the sessions screen uses for backup and
 * management. Same discipline as [TerminalSessionsSource]: `:feature-terminal`
 * cannot see the generated client, so its tests fake this interface
 * and not `MobileApi`.
 */
interface TerminalBackupSource {
    suspend fun backups(): BackupsResult

    /** A null [session] saves ALL visible sessions in one package. */
    suspend fun createBackup(session: String? = null): ActionResult

    /** A null [session] restores every session in the backup. */
    suspend fun restore(id: String, session: String? = null): ActionResult

    /** A null [session] deletes the whole backup; given one, only that session within it. */
    suspend fun deleteBackup(id: String, session: String? = null): ActionResult

    suspend fun renameSession(from: String, to: String): ActionResult

    /**
     * Kills the session and everything running inside it.
     *
     * Irreversible by design and with no safety net on the server side — it is
     * the caller that has to confirm first.
     */
    suspend fun killSession(name: String): ActionResult

    /**
     * Changes WHO the session appears to in the day-to-day list. [target] is a
     * username, or `"*"` for everyone. Admin only.
     */
    suspend fun assignSession(name: String, target: String): ActionResult

    /**
     * The session's last lines, as plain text, without attaching to it.
     *
     * It is what answers "what is going on in there?" without the cost of
     * opening: opening an agent session changes what it shows, and on a phone
     * opening to look and going back is expensive.
     */
    suspend fun sessionPreview(name: String, lines: Int = 20): PreviewResult

    /**
     * Who a session CAN be assigned to.
     *
     * It exists so the screen can OFFER the list. A free-text field would fail
     * silently: the server accepts any target, and a username with one
     * character wrong makes the session disappear from everybody's list, with
     * no error.
     */
    suspend fun assignmentTargets(): TargetsResult
}

/** Resultado de [TerminalBackupSource.assignmentTargets]. */
sealed interface TargetsResult {
    data class Success(val targets: List<String>) : TargetsResult
    data class Error(val reason: String) : TargetsResult
}

/** Resultado de [TerminalBackupSource.sessionPreview]. */
sealed interface PreviewResult {
    data class Success(val text: String, val lines: Int) : PreviewResult
    data class Error(val reason: String) : PreviewResult
}
