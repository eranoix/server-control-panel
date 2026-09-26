package dev.servercontrolpanel.data.terminal

/**
 * One session inside a backup. [summary] is a one-line description produced by
 * the server (which holds the scrollback), so the user can pick what to restore.
 */
data class BackupSession(val name: String, val summary: String, val lines: Int)

/**
 * A backup of terminal sessions.
 *
 * @property origin `manual`, `auto` (periodic collector) or `scheduled`. Shown on
 *   screen because automatic backups are pruned and manual ones are not.
 * @property bytes size on disk; backups with long histories can exceed 10 MB.
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
 * The result of an operation that changes something (create, restore, delete,
 * rename). A ready-made sentence rather than a boolean, since partial results
 * (restoring 3 of 5 sessions) are common.
 */
sealed interface ActionResult {
    data class Ok(val message: String) : ActionResult
    data class Error(val reason: String) : ActionResult

    /**
     * No network, so the action was stored and goes out when the internet is
     * back. Neither an error (the user would repeat it and create a duplicate) nor
     * an [Ok] (the result does not exist yet).
     */
    data class Queued(val message: String) : ActionResult
}

/**
 * The slice of [TerminalRepository] the sessions screen uses for backup and
 * management. `:feature-terminal` cannot see the generated client, so its tests
 * fake this interface.
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

    /** Kills the session and everything in it. Irreversible, so the caller must confirm first. */
    suspend fun killSession(name: String): ActionResult

    /**
     * Changes WHO the session appears to in the day-to-day list. [target] is a
     * username, or `"*"` for everyone. Admin only.
     */
    suspend fun assignSession(name: String, target: String): ActionResult

    /**
     * The session's last lines as plain text, without attaching (attaching to an
     * agent session changes what it shows).
     */
    suspend fun sessionPreview(name: String, lines: Int = 20): PreviewResult

    /**
     * Who a session can be assigned to, so the screen offers a list: the server
     * accepts any target, and a mistyped username would hide the session silently.
     */
    suspend fun assignmentTargets(): TargetsResult
}

/** Result of [TerminalBackupSource.assignmentTargets]. */
sealed interface TargetsResult {
    data class Success(val targets: List<String>) : TargetsResult
    data class Error(val reason: String) : TargetsResult
}

/** Result of [TerminalBackupSource.sessionPreview]. */
sealed interface PreviewResult {
    data class Success(val text: String, val lines: Int) : PreviewResult
    data class Error(val reason: String) : PreviewResult
}
