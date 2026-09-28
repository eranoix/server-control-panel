package dev.servercontrolpanel.data.terminal

data class BackupVersion(
    val id: String,
    val createdAt: Long,
    val origin: String?,
    val bytes: Long,
    val summary: String,
    val sessionsInBackup: Int,
)

data class BackupGroup(
    val session: String,
    val summary: String,
    val versions: List<BackupVersion>,
)

fun groupBySession(backups: List<SessionBackup>): List<BackupGroup> {
    val bySession = LinkedHashMap<String, MutableList<BackupVersion>>()
    backups.forEach { backup ->
        backup.sessions.forEach { session ->
            if (session.name.isBlank()) return@forEach
            bySession.getOrPut(session.name) { mutableListOf() } += BackupVersion(
                id = backup.id,
                createdAt = backup.createdAt,
                origin = backup.origin,
                bytes = backup.bytes,
                summary = session.summary,
                sessionsInBackup = backup.sessions.size,
            )
        }
    }
    return bySession.entries
        .map { (name, versions) ->
            val sorted = versions.sortedByDescending { it.createdAt }
            BackupGroup(
                session = name,
                summary = sorted.firstOrNull { it.summary.isNotBlank() }?.summary.orEmpty(),
                versions = sorted,
            )
        }
        .sortedBy { it.session.lowercase() }
}
