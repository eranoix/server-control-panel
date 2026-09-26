package dev.servercontrolpanel.feature.terminal.geometry

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * Every distinct grid size the server receives is a `SIGWINCH`, and each one can
 * duplicate the terminal history, so these tests count grid sizes rather than check
 * single values. [GridGeometry] is a stateless measurement: keeping a maximum would
 * also keep spurious sizes from navigation animations.
 */
class GridGeometryTest {

    private val heightWithKeyboardClosedPx = 1840
    private val navBarPx = 63
    private val cellH = 40
    private val cellW = 16

    /** The height the layout offers (after `imePadding()` in `AppNavHost`) for an IME inset of [imePx]. */
    private fun offeredHeight(imePx: Int) =
        heightWithKeyboardClosedPx - (imePx - navBarPx).coerceAtLeast(0)

    /** Intermediate insets while the keyboard opens and closes must never reach the server. */
    @Test
    fun `the whole keyboard animation does not change the row count`() {
        val rowsSeen = mutableSetOf<Int>()

        // From keyboard closed (0) to open (740), frame by frame, and back.
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

    /** With the keyboard closed the calculation must be the identity. */
    @Test
    fun `without a keyboard nothing is added and nothing is covered`() {
        assertEquals(
            heightWithKeyboardClosedPx,
            GridGeometry.heightWithoutKeyboard(heightWithKeyboardClosedPx, 0, navBarPx),
        )
        assertEquals(0, GridGeometry.coveredByKeyboard(0, navBarPx))
    }

    /**
     * The IME inset includes the navigation bar, which `imePadding()` already consumed;
     * adding the raw inset would count it twice and push the grid off the top.
     */
    @Test
    fun `the navigation bar is not counted twice`() {
        val ime = 740
        val full = GridGeometry.heightWithoutKeyboard(offeredHeight(ime), ime, navBarPx)

        assertEquals(heightWithKeyboardClosedPx, full)
        assertEquals(ime - navBarPx, GridGeometry.coveredByKeyboard(ime, navBarPx))
    }

    /** An inset smaller than the navigation bar must not yield a negative offset, which would hide the cursor. */
    @Test
    fun `an inset smaller than the bar does not become a negative offset`() {
        assertEquals(0, GridGeometry.coveredByKeyboard(imePx = 20, navBarPx = 63))
        assertEquals(
            heightWithKeyboardClosedPx,
            GridGeometry.heightWithoutKeyboard(heightWithKeyboardClosedPx, 20, 63),
        )
    }

    /** Banners and bars really change the height, and the grid must follow them. */
    @Test
    fun `a real height change changes the row count`() {
        val withBand = GridGeometry.heightWithoutKeyboard(heightWithKeyboardClosedPx - 120, 0, navBarPx)
        val withoutBand = GridGeometry.heightWithoutKeyboard(heightWithKeyboardClosedPx, 0, navBarPx)

        assertEquals((heightWithKeyboardClosedPx - 120) / cellH, GridGeometry.lines(withBand, cellH))
        assertEquals(heightWithKeyboardClosedPx / cellH, GridGeometry.lines(withoutBand, cellH))
    }

    /** No memory: a spurious measurement affects only its own frame. */
    @Test
    fun `a spurious measurement does not affect the following ones`() {
        GridGeometry.heightWithoutKeyboard(9_000, 0, navBarPx)
        val after = GridGeometry.heightWithoutKeyboard(heightWithKeyboardClosedPx, 0, navBarPx)

        assertEquals(heightWithKeyboardClosedPx, after)
    }

    /** Changing the font size changes the grid on purpose. */
    @Test
    fun `changing the cell size changes the row count`() {
        assertEquals(heightWithKeyboardClosedPx / 20, GridGeometry.lines(heightWithKeyboardClosedPx, 20))
        assertEquals(heightWithKeyboardClosedPx / 60, GridGeometry.lines(heightWithKeyboardClosedPx, 60))
    }

    /** A degenerate window must not produce a grid of zero rows. */
    @Test
    fun `a window smaller than one cell still yields one row and one column`() {
        assertEquals(1, GridGeometry.columns(4, cellW))
        assertEquals(1, GridGeometry.lines(4, cellH))
    }
}
