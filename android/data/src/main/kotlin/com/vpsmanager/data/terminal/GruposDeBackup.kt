package com.vpsmanager.data.terminal

/**
 * One saved version of ONE session: the date that snapshot was taken.
 *
 * [sessoesNoBackup] exists so the screen can tell apart two things that look
 * alike and are not: a backup that saved only this session, and a backup of
 * the whole server of which this session is one part. The difference changes
 * what "restore" means and what [bytes] refers to.
 */
data class VersaoDeBackup(
    val id: String,
    val criadoEm: Long,
    val origem: String?,
    /** Size of the WHOLE archive, not of this session's slice. See [sessoesNoBackup]. */
    val bytes: Long,
    val resumo: String,
    val sessoesNoBackup: Int,
)

/**
 * Every saved version of one session, newest first.
 */
data class GrupoDeBackup(
    val sessao: String,
    /** The summary of the most recent version that has one — see [agruparPorSessao]. */
    val resumo: String,
    val versoes: List<VersaoDeBackup>,
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
fun agruparPorSessao(backups: List<SessionBackup>): List<GrupoDeBackup> {
    val porSessao = LinkedHashMap<String, MutableList<VersaoDeBackup>>()
    backups.forEach { backup ->
        backup.sessoes.forEach { sessao ->
            if (sessao.nome.isBlank()) return@forEach
            porSessao.getOrPut(sessao.nome) { mutableListOf() } += VersaoDeBackup(
                id = backup.id,
                criadoEm = backup.criadoEm,
                origem = backup.origem,
                bytes = backup.bytes,
                resumo = sessao.resumo,
                sessoesNoBackup = backup.sessoes.size,
            )
        }
    }
    return porSessao.entries
        .map { (nome, versoes) ->
            val ordenadas = versoes.sortedByDescending { it.criadoEm }
            GrupoDeBackup(
                sessao = nome,
                resumo = ordenadas.firstOrNull { it.resumo.isNotBlank() }?.resumo.orEmpty(),
                versoes = ordenadas,
            )
        }
        // String `compareTo` orders by code point: "Vpsm" would come before
        // "main" because uppercase is smaller. In a list of names chosen by
        // people that reads as a bug — hence the case-insensitive comparison.
        .sortedBy { it.sessao.lowercase() }
}
