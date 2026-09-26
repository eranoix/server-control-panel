package com.vpsmanager.data.terminal

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** Backups are grouped by session, since people look for a session's earlier version, not for an archive. */
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

    /** One snapshot with several sessions becomes one group per session. */
    @Test
    fun `a snapshot with several sessions becomes one group per session`() {
        val groups = groupBySession(
            listOf(backup("b1", 100, "main" to "", "Vpsm" to "", "proxy" to "")),
        )

        assertEquals(3, groups.size)
        assertEquals(listOf("main", "proxy", "Vpsm"), groups.map { it.session })
        // Each group has one version, from the snapshot the session was in.
        assertTrue(groups.all { it.versions.size == 1 })
    }

    /** The same session in three snapshots becomes three versions of a single group. */
    @Test
    fun `the same session in several snapshots becomes versions of one group`() {
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

    /** The newest version goes on top. */
    @Test
    fun `versions go from newest to oldest`() {
        val groups = groupBySession(
            listOf(
                backup("b1", 100, "main" to ""),
                backup("b2", 300, "main" to ""),
                backup("b3", 200, "main" to ""),
            ),
        )

        assertEquals(listOf(300L, 200L, 100L), groups.single().versions.map { it.createdAt })
    }

    /** Case-insensitive alphabetical order; plain `compareTo` would put "Vpsm" before "main". */
    @Test
    fun `groups are alphabetical ignoring case`() {
        val groups = groupBySession(
            listOf(backup("b1", 100, "Vpsm" to "", "main" to "", "Aplicativo" to "")),
        )

        assertEquals(listOf("Aplicativo", "main", "Vpsm"), groups.map { it.session })
    }

    /** The summary identifies a session with an unhelpful name, so an empty newest summary must not hide it. */
    @Test
    fun `the group summary comes from the newest version that has one`() {
        val groups = groupBySession(
            listOf(
                backup("b1", 300, "main" to ""),
                backup("b2", 200, "main" to "running the deploy"),
                backup("b3", 100, "main" to "something older"),
            ),
        )

        assertEquals("running the deploy", groups.single().summary)
    }

    /**
     * `sessionsInBackup` drives the "from a backup with N sessions" size label and the
     * "restore all" button, so the size is not attributed to a single session.
     */
    @Test
    fun `each version knows how many sessions its snapshot had`() {
        val groups = groupBySession(
            listOf(backup("b1", 100, "main" to "", "Vpsm" to "", bytes = 89_000L)),
        )

        assertTrue(groups.all { it.versions.single().sessionsInBackup == 2 })
        assertEquals(89_000L, groups.first().versions.single().bytes)
    }

    /** An empty name does not become a ghost group. */
    @Test
    fun `a session without a name is dropped`() {
        val groups = groupBySession(listOf(backup("b1", 100, "" to "", "main" to "")))

        assertEquals(listOf("main"), groups.map { it.session })
    }

    @Test
    fun `an empty list produces no group`() {
        assertTrue(groupBySession(emptyList()).isEmpty())
    }
}
