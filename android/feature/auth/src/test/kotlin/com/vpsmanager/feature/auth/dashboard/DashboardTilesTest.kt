package com.vpsmanager.feature.auth.dashboard

import com.vpsmanager.data.dashboard.DashboardTarget
import com.vpsmanager.data.dashboard.Severity
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * What these tests protect: the grid is the first thing a person looks at
 * when they open the app in a hurry. Two of its guarantees cannot be just a
 * sentence in the KDoc — that it **does not hide a fire**, and that it does
 * not scramble the alignment of the columns.
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

    // ── the rule that matters most ──────────────────────────────────────────

    /**
     * The real case: the person assembles the dashboard on a good day with CPU
     * and memory, the disk fills up, and the disk was not on the list. A
     * dashboard that stays green in that scenario is worse than none at all,
     * because it is consulted with confidence.
     */
    @Test
    fun `bloco CRITICO entra na grade mesmo sem ter sido escolhido`() {
        val catalog = listOf(
            tile("cpu"),
            tile("memoria"),
            tile("disco", severity = Severity.CRITICAL),
        )

        val visible = visibleTiles(catalog, chosen = listOf("cpu", "memoria"))

        assertTrue("o disco crítico tem de aparecer", visible.any { it.id == "disco" })
    }

    /** And it goes in FIRST: a fire at the foot of a scrollable dashboard is unseen. */
    @Test
    fun `o critico vai para a frente da grade`() {
        val catalog = listOf(
            tile("cpu"),
            tile("memoria"),
            tile("disco", severity = Severity.CRITICAL),
        )

        val visible = visibleTiles(catalog, chosen = listOf("cpu", "memoria"))

        assertEquals("disco", visible.first().id)
    }

    /** A critical one that was ALSO chosen must not show up twice. */
    @Test
    fun `critico escolhido aparece uma vez so`() {
        val catalog = listOf(tile("cpu", severity = Severity.CRITICAL), tile("memoria"))

        val visible = visibleTiles(catalog, chosen = listOf("cpu", "memoria"))

        assertEquals(listOf("cpu", "memoria"), visible.map { it.id })
    }

    /**
     * ATTENTION does not force its way in. The distinction is the same as that
     * of the two threshold bands: "look today" fits within the person's
     * choice, "look now" does not. Without this, a machine with a long uptime
     * (swap in attention the whole time) would clog the dashboard with blocks
     * nobody asked for.
     */
    @Test
    fun `ATENCAO nao força entrada na grade`() {
        val catalog = listOf(tile("cpu"), tile("swap", severity = Severity.WARNING))

        val visible = visibleTiles(catalog, chosen = listOf("cpu"))

        assertEquals(listOf("cpu"), visible.map { it.id })
    }

    /** A stored id that vanished from the catalogue leaves no tombstone on screen. */
    @Test
    fun `id que nao existe mais no catalogo simplesmente some`() {
        val catalog = listOf(tile("cpu"))

        val visible = visibleTiles(catalog, chosen = listOf("cpu", "disco:/antigo"))

        assertEquals(listOf("cpu"), visible.map { it.id })
    }

    /** The order of the choice IS the choice — it is what puts what matters on top. */
    @Test
    fun `a ordem escolhida e preservada`() {
        val catalog = listOf(tile("a"), tile("b"), tile("c"))

        val visible = visibleTiles(catalog, chosen = listOf("c", "a", "b"))

        assertEquals(listOf("c", "a", "b"), visible.map { it.id })
    }

    // ── the alignment of the columns ────────────────────────────────────────

    @Test
    fun `dois estreitos por linha`() {
        val lines = gridRows(listOf(tile("a"), tile("b"), tile("c")))

        assertEquals(2, lines[0].size)
        assertEquals(1, lines[1].size)
    }

    /**
     * A wide block CLOSES the row in progress before going in. Without that, a
     * wide one after a narrow one would produce a row of three weights and the
     * grid would lose its vertical alignment — which is precisely what lets
     * you compare two numbers at a glance.
     */
    @Test
    fun `o largo nunca divide linha com um estreito`() {
        val lines = gridRows(listOf(tile("a"), tile("largo", wide = true), tile("b")))

        assertEquals(listOf("a"), lines[0].map { it.id })
        assertEquals(listOf("largo"), lines[1].map { it.id })
        assertEquals(listOf("b"), lines[2].map { it.id })
    }

    @Test
    fun `grade vazia nao produz linha vazia`() {
        assertTrue(gridRows(emptyList()).isEmpty())
    }
}
