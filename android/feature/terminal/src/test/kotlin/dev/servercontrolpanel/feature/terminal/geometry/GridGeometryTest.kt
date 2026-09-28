package dev.servercontrolpanel.feature.terminal.geometry

import org.junit.Assert.assertEquals
import org.junit.Test

class GridGeometryTest {

    private val heightWithKeyboardClosedPx = 1840
    private val navBarPx = 63
    private val cellH = 40
    private val cellW = 16

    private fun offeredHeight(imePx: Int) =
        heightWithKeyboardClosedPx - (imePx - navBarPx).coerceAtLeast(0)

    @Test
    fun `the whole keyboard animation does not change the row count`() {
        val rowsSeen = mutableSetOf<Int>()

        val insets = listOf(0, 120, 260, 400, 510, 600, 660, 700, 720, 740)
        (insets + insets.reversed()).forEach { ime ->
            val full = GridGeometry.heightWithoutKeyboard(
                availableHeightPx = offeredHeight(ime),
                imePx = ime,
                navBarPx = navBarPx,
            )
            rowsSeen += GridGeometry.lines(full, cellH)
        }

        assertEquals(
            "each extra row count in this set is a SIGWINCH, and each SIGWINCH copies the history",
            setOf(heightWithKeyboardClosedPx / cellH),
            rowsSeen,
        )
    }

    @Test
    fun `without a keyboard nothing is added and nothing is covered`() {
        assertEquals(
            heightWithKeyboardClosedPx,
            GridGeometry.heightWithoutKeyboard(heightWithKeyboardClosedPx, 0, navBarPx),
        )
        assertEquals(0, GridGeometry.coveredByKeyboard(0, navBarPx))
    }

    @Test
    fun `the navigation bar is not counted twice`() {
        val ime = 740
        val full = GridGeometry.heightWithoutKeyboard(offeredHeight(ime), ime, navBarPx)

        assertEquals(heightWithKeyboardClosedPx, full)
        assertEquals(ime - navBarPx, GridGeometry.coveredByKeyboard(ime, navBarPx))
    }

    @Test
    fun `an inset smaller than the bar does not become a negative offset`() {
        assertEquals(0, GridGeometry.coveredByKeyboard(imePx = 20, navBarPx = 63))
        assertEquals(
            heightWithKeyboardClosedPx,
            GridGeometry.heightWithoutKeyboard(heightWithKeyboardClosedPx, 20, 63),
        )
    }

    @Test
    fun `a real height change changes the row count`() {
        val withBand = GridGeometry.heightWithoutKeyboard(heightWithKeyboardClosedPx - 120, 0, navBarPx)
        val withoutBand = GridGeometry.heightWithoutKeyboard(heightWithKeyboardClosedPx, 0, navBarPx)

        assertEquals((heightWithKeyboardClosedPx - 120) / cellH, GridGeometry.lines(withBand, cellH))
        assertEquals(heightWithKeyboardClosedPx / cellH, GridGeometry.lines(withoutBand, cellH))
    }

    @Test
    fun `a spurious measurement does not affect the following ones`() {
        GridGeometry.heightWithoutKeyboard(9_000, 0, navBarPx)
        val after = GridGeometry.heightWithoutKeyboard(heightWithKeyboardClosedPx, 0, navBarPx)

        assertEquals(heightWithKeyboardClosedPx, after)
    }

    @Test
    fun `changing the cell size changes the row count`() {
        assertEquals(heightWithKeyboardClosedPx / 20, GridGeometry.lines(heightWithKeyboardClosedPx, 20))
        assertEquals(heightWithKeyboardClosedPx / 60, GridGeometry.lines(heightWithKeyboardClosedPx, 60))
    }

    @Test
    fun `a window smaller than one cell still yields one row and one column`() {
        assertEquals(1, GridGeometry.columns(4, cellW))
        assertEquals(1, GridGeometry.lines(4, cellH))
    }
}
