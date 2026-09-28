package dev.servercontrolpanel.data.terminal

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

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

    @Test
    fun `a snapshot with several sessions becomes one group per session`() {
        val groups = groupBySession(
            listOf(backup("b1", 100, "main" to "", "Web" to "", "proxy" to "")),
        )

        assertEquals(3, groups.size)
        assertEquals(listOf("main", "proxy", "Web"), groups.map { it.session })
        assertTrue(groups.all { it.versions.size == 1 })
    }

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

    @Test
    fun `groups are alphabetical ignoring case`() {
        val groups = groupBySession(
            listOf(backup("b1", 100, "Web" to "", "main" to "", "App" to "")),
        )

        assertEquals(listOf("App", "main", "Web"), groups.map { it.session })
    }

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

    @Test
    fun `each version knows how many sessions its snapshot had`() {
        val groups = groupBySession(
            listOf(backup("b1", 100, "main" to "", "Web" to "", bytes = 89_000L)),
        )

        assertTrue(groups.all { it.versions.single().sessionsInBackup == 2 })
        assertEquals(89_000L, groups.first().versions.single().bytes)
    }

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
