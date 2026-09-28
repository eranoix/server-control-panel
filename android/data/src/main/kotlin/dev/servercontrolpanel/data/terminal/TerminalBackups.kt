package dev.servercontrolpanel.data.terminal

data class BackupSession(val name: String, val summary: String, val lines: Int)

data class SessionBackup(
    val id: String,
    val createdAt: Long,
    val origin: String?,
    val bytes: Long,
    val sessions: List<BackupSession>,
)

sealed interface BackupsResult {
    data class Success(val backups: List<SessionBackup>) : BackupsResult
    data object Empty : BackupsResult
    data class Error(val reason: String) : BackupsResult
}

sealed interface ActionResult {
    data class Ok(val message: String) : ActionResult
    data class Error(val reason: String) : ActionResult

    data class Queued(val message: String) : ActionResult
}

interface TerminalBackupSource {
    suspend fun backups(): BackupsResult

    suspend fun createBackup(session: String? = null): ActionResult

    suspend fun restore(id: String, session: String? = null): ActionResult

    suspend fun deleteBackup(id: String, session: String? = null): ActionResult

    suspend fun renameSession(from: String, to: String): ActionResult

    suspend fun killSession(name: String): ActionResult

    suspend fun assignSession(name: String, target: String): ActionResult

    suspend fun sessionPreview(name: String, lines: Int = 20): PreviewResult

    suspend fun assignmentTargets(): TargetsResult
}

sealed interface TargetsResult {
    data class Success(val targets: List<String>) : TargetsResult
    data class Error(val reason: String) : TargetsResult
}

sealed interface PreviewResult {
    data class Success(val text: String, val lines: Int) : PreviewResult
    data class Error(val reason: String) : PreviewResult
}
