package com.vpsmanager.data.terminal

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * What these tests protect: the backups sheet was organised by the structure of
 * the ARCHIVE — a scheduled backup that saved eight sessions turned into eight
 * identical cards showing the same date. The owner called it a mess, and he was
 * right: nobody looks for a backup by archive, they look for *"the `main`
 * session from before I broke everything"*.
 */
class BackupGroupsTest {

    private fun backup(
        id: String,
        createdAt: Long,
        vararg sessions: Pair<String, String>,
        bytes: Long = 1_000L,
        origin: String = "scheduled",
    ) = SessionBackup(
        id = id,
        createdAt = createdAt,
        origin = origin,
        bytes = bytes,
        sessions = sessions.map { (name, summary) -> BackupSession(name, summary, 0) },
    )

    /** The case in the photo: one snapshot with eight sessions turned into eight cards. */
    @Test
    fun `um snapshot de varias sessoes vira um grupo por sessao, nao um cartao por sessao`() {
        val groups = groupBySession(
            listOf(backup("b1", 100, "main" to "", "Vpsm" to "", "proxy" to "")),
        )

        assertEquals(3, groups.size)
        assertEquals(listOf("main", "proxy", "Vpsm"), groups.map { it.session })
        // Each group has ONE version — the one from the snapshot the session was in.
        assertTrue(groups.all { it.versions.size == 1 })
    }

    /** The same session in three snapshots becomes three versions of a single group. */
    @Test
    fun `a mesma sessao em varios snapshots vira versoes de um grupo`() {
        val groups = groupBySession(
            listOf(
                backup("b1", 300, "main" to ""),
                backup("b2", 100, "main" to ""),
                backup("b3", 200, "main" to ""),
            ),
        )

        assertEquals(1, groups.size)
        assertEquals(3, groups.single().versions.size)
    }

    /** The version you want is almost always the last good one: it goes on top. */
    @Test
    fun `as versoes vem da mais nova para a mais velha`() {
        val groups = groupBySession(
            listOf(
                backup("b1", 100, "main" to ""),
                backup("b2", 300, "main" to ""),
                backup("b3", 200, "main" to ""),
            ),
        )

        assertEquals(listOf(300L, 200L, 100L), groups.single().versions.map { it.createdAt })
    }

    /**
     * Alphabetical order, CASE-INSENSITIVE. String `compareTo` orders by code
     * point, so "Vpsm" would come before "main" — in a list of names chosen by
     * people, that reads as a bug.
     */
    @Test
    fun `os grupos sao alfabeticos ignorando maiuscula`() {
        val groups = groupBySession(
            listOf(backup("b1", 100, "Vpsm" to "", "main" to "", "Aplicativo" to "")),
        )

        assertEquals(listOf("Aplicativo", "main", "Vpsm"), groups.map { it.session })
    }

    /**
     * The group summary is the clue to WHICH session that is when the name does
     * not say (`tt`, `proxy`). Inheriting the empty one from the most recent
     * version would hide the only textual clue there is.
     */
    @Test
    fun `o resumo do grupo vem da versao mais recente QUE TENHA um`() {
        val groups = groupBySession(
            listOf(
                backup("b1", 300, "main" to ""),
                backup("b2", 200, "main" to "rodando o deploy"),
                backup("b3", 100, "main" to "outra coisa mais velha"),
            ),
        )

        assertEquals("rodando o deploy", groups.single().summary)
    }

    /**
     * `sessionsInBackup` decides two sentences on the screen: whether the size
     * reads "from a backup with N sessions", and whether the "restore all"
     * button shows up. Without it, "89 kB" next to ONE session would suggest
     * that session takes up 89 kB.
     */
    @Test
    fun `cada versao sabe quantas sessoes havia no snapshot dela`() {
        val groups = groupBySession(
            listOf(backup("b1", 100, "main" to "", "Vpsm" to "", bytes = 89_000L)),
        )

        assertTrue(groups.all { it.versions.single().sessionsInBackup == 2 })
        assertEquals(89_000L, groups.first().versions.single().bytes)
    }

    /** An empty name does not become a ghost group. */
    @Test
    fun `sessao sem nome e descartada`() {
        val groups = groupBySession(listOf(backup("b1", 100, "" to "", "main" to "")))

        assertEquals(listOf("main"), groups.map { it.session })
    }

    @Test
    fun `lista vazia nao produz grupo`() {
        assertTrue(groupBySession(emptyList()).isEmpty())
    }
}
