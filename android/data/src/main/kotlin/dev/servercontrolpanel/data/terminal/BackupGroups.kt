package dev.servercontrolpanel.data.terminal

/**
 * One saved version of ONE session: the date that snapshot was taken.
 *
 * [sessionsInBackup] exists so the screen can tell apart two things that look
 * alike and are not: a backup that saved only this session, and a backup of
 * the whole server of which this session is one part. The difference changes
 * what "restore" means and what [bytes] refers to.
 */
data class BackupVersion(
    val id: String,
    val createdAt: Long,
    val origin: String?,
    /** Size of the WHOLE archive, not of this session's slice. See [sessionsInBackup]. */
    val bytes: Long,
    val summary: String,
    val sessionsInBackup: Int,
)

/**
 * Every saved version of one session, newest first.
 */
data class BackupGroup(
    val session: String,
    /** The summary of the most recent version that has one — see [groupBySession]. */
    val summary: String,
    val versions: List<BackupVersion>,
)

/**
 * Turns the backup list inside out: from "snapshots that contain sessions" to
 * "sessions that have versions".
 *
 * ## Why the inversion is the right screen
 *
 * The server stores SNAPSHOTS: a backup scheduled for 08:40 saves the eight
 * live sessions into a single archive. Listing that the way it arrives
 * produces, on screen, eight identical cards saying "09/09 08:40 · scheduled ·
 * 1 session" — and that wall of repeated dates is exactly what the owner called
 * a mess.
 *
 * The mistake is not one of style: it is that the list is organised by the
 * structure of the ARCHIVE, and nobody looks for a backup by archive. The real
 * question is always *"I want the `main` session from before I broke
 * everything"* — name first, date second. Grouping by session puts the screen
 * in the order of the question.
 *
 * ## The three orderings, and why each one
 *
 * - **Groups by name**, alphabetical: the list of sessions is stable between
 *   openings, so the eye learns where each one sits. Sorting by "most recent"
 *   would make the groups dance on every automatic backup.
 * - **Versions newest to oldest**: the version you want is almost always the
 *   last good one, and it has to be at the top without scrolling.
 * - **Group summary** = the one from the most recent version THAT HAS ONE. A
 *   snapshot of an idle session may arrive with no summary; inheriting the
 *   empty one would hide the only textual clue to which session that is.
 */
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
        // String `compareTo` orders by code point: "Panel" would come before
        // "main" because uppercase is smaller. In a list of names chosen by
        // people that reads as a bug — hence the case-insensitive comparison.
        .sortedBy { it.session.lowercase() }
}
