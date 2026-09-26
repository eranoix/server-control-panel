package com.vpsmanager.feature.auth.dashboard

import com.vpsmanager.data.dashboard.DashboardTarget
import com.vpsmanager.data.dashboard.Severity
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Guards two grid guarantees: it never hides a critical tile, and it keeps
 * columns aligned.
 */
class DashboardTilesTest {

    private fun tile(
        id: String,
        severity: Severity = Severity.OK,
        wide: Boolean = false,
    ) = DashboardTile(
        id = id,
        label = id,
        value = "1",
        sub = "",
        severity = severity,
        target = DashboardTarget.METRICS,
        wide = wide,
    )

    /** A disk that fills up must show even if the user never chose its tile. */
    @Test
    fun `a CRITICAL tile joins the grid even if not chosen`() {
        val catalog = listOf(
            tile("cpu"),
            tile("memoria"),
            tile("disco", severity = Severity.CRITICAL),
        )

        val visible = visibleTiles(catalog, chosen = listOf("cpu", "memoria"))

        assertTrue("the critical disk must appear", visible.any { it.id == "disco" })
    }

    /** It goes first, since a problem at the bottom of a scrolled grid goes unseen. */
    @Test
    fun `a critical tile goes to the front of the grid`() {
        val catalog = listOf(
            tile("cpu"),
            tile("memoria"),
            tile("disco", severity = Severity.CRITICAL),
        )

        val visible = visibleTiles(catalog, chosen = listOf("cpu", "memoria"))

        assertEquals("disco", visible.first().id)
    }

    @Test
    fun `a chosen critical tile appears only once`() {
        val catalog = listOf(tile("cpu", severity = Severity.CRITICAL), tile("memoria"))

        val visible = visibleTiles(catalog, chosen = listOf("cpu", "memoria"))

        assertEquals(listOf("cpu", "memoria"), visible.map { it.id })
    }

    /** WARNING is not forced in, or long-lived warnings would clutter the grid. */
    @Test
    fun `WARNING does not force a tile into the grid`() {
        val catalog = listOf(tile("cpu"), tile("swap", severity = Severity.WARNING))

        val visible = visibleTiles(catalog, chosen = listOf("cpu"))

        assertEquals(listOf("cpu"), visible.map { it.id })
    }

    @Test
    fun `an id no longer in the catalog is simply dropped`() {
        val catalog = listOf(tile("cpu"))

        val visible = visibleTiles(catalog, chosen = listOf("cpu", "disco:/old"))

        assertEquals(listOf("cpu"), visible.map { it.id })
    }

    @Test
    fun `the chosen order is preserved`() {
        val catalog = listOf(tile("a"), tile("b"), tile("c"))

        val visible = visibleTiles(catalog, chosen = listOf("c", "a", "b"))

        assertEquals(listOf("c", "a", "b"), visible.map { it.id })
    }

    @Test
    fun `two narrow tiles per row`() {
        val lines = gridRows(listOf(tile("a"), tile("b"), tile("c")))

        assertEquals(2, lines[0].size)
        assertEquals(1, lines[1].size)
    }

    /** A wide tile closes the current row first, keeping columns aligned. */
    @Test
    fun `a wide tile never shares a row with a narrow one`() {
        val lines = gridRows(listOf(tile("a"), tile("wide", wide = true), tile("b")))

        assertEquals(listOf("a"), lines[0].map { it.id })
        assertEquals(listOf("wide"), lines[1].map { it.id })
        assertEquals(listOf("b"), lines[2].map { it.id })
    }

    @Test
    fun `an empty grid produces no empty row`() {
        assertTrue(gridRows(emptyList()).isEmpty())
    }
}
