package dev.servercontrolpanel.feature.terminal.geometry

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * [ScreenAnchor] decides where the compose box appears: the end of the content must
 * sit at the bottom of the visible area, neither hidden by the keyboard nor floating.
 */
class ScreenAnchorTest {

    private val cell = 30

    @Test
    fun `a short frame moves down to touch the bottom`() {
        // 20 content lines in a 52-line area: the blank space goes above.
        val offset = ScreenAnchor.offsetY(
            contentBottomPx = 20 * cell,
            visibleHeightPx = 52 * cell,
            maxLiftPx = 0,
        )
        assertEquals("moved down exactly enough to touch the bottom", 32 * cell, offset)
        assertTrue("positive means down", offset > 0)
    }

    @Test
    fun `a frame exactly the size of the area does not move`() {
        assertEquals(
            0,
            ScreenAnchor.offsetY(
                contentBottomPx = 52 * cell,
                visibleHeightPx = 52 * cell,
                maxLiftPx = 0,
            ),
        )
    }

    @Test
    fun `with the keyboard up the frame moves up, at most by what is covered`() {
        // 52 grid lines, only 30 visible: the end is below the visible area.
        val covered = 22 * cell
        val offset = ScreenAnchor.offsetY(
            contentBottomPx = 52 * cell,
            visibleHeightPx = 30 * cell,
            maxLiftPx = covered,
        )
        assertEquals(-22 * cell, offset)
    }

    @Test
    fun `never moves up more than the grid outside the area`() {
        // Moving further would pull the grid out with nothing to fill its place.
        val offset = ScreenAnchor.offsetY(
            contentBottomPx = 200 * cell,
            visibleHeightPx = 30 * cell,
            maxLiftPx = 5 * cell,
        )
        assertEquals(-5 * cell, offset)
    }

    @Test
    fun `the last useful row keeps the slack below the cursor`() {
        // Cursor on line 10, content ending on 9: show up to line 12 so the input
        // box's bottom edge stays visible.
        assertEquals(
            12,
            ScreenAnchor.lastUsefulRow(
                lastRowWithContent = 9,
                cursorRow = 10,
                slackBelowCursor = 2,
                lines = 52,
            ),
        )
    }

    @Test
    fun `content below the cursor wins over the cursor`() {
        // A footer drawn below the box defines the end.
        assertEquals(
            30,
            ScreenAnchor.lastUsefulRow(
                lastRowWithContent = 30,
                cursorRow = 20,
                slackBelowCursor = 2,
                lines = 52,
            ),
        )
    }

    @Test
    fun `the slack never points outside the grid`() {
        assertEquals(
            51,
            ScreenAnchor.lastUsefulRow(
                lastRowWithContent = 51,
                cursorRow = 51,
                slackBelowCursor = 2,
                lines = 52,
            ),
        )
    }

    @Test
    fun `an empty grid does not shift anything`() {
        // Falling back to the cursor (line 0) would drop an empty frame to a two-line
        // strip at the bottom with blank app surface above it.
        val last = ScreenAnchor.lastUsefulRow(
            lastRowWithContent = -1,
            cursorRow = 0,
            slackBelowCursor = 2,
            lines = 52,
        )
        assertEquals("an empty grid anchors at its own end", 51, last)
        assertEquals(
            "and therefore does not shift",
            0,
            ScreenAnchor.offsetY(
                contentBottomPx = (last + 1) * cell,
                visibleHeightPx = 52 * cell,
                maxLiftPx = 0,
            ),
        )
    }

    @Test
    fun `finds the last row with content scanning from the bottom`() {
        // 10x5 grid with something only on line 2.
        val found = ScreenAnchor.lastRowWithContent(columns = 10, lines = 5) { x, y ->
            !(y == 2 && x == 3)
        }
        assertEquals(2, found)
    }

    @Test
    fun `an entirely empty grid returns minus one, not zero`() {
        // -1 means "nothing to anchor to"; 0 would mean content on the top line.
        assertEquals(-1, ScreenAnchor.lastRowWithContent(columns = 10, lines = 5) { _, _ -> true })
    }

    @Test
    fun `a space counts as empty`() {
        val found = ScreenAnchor.lastRowWithContent(columns = 10, lines = 3) { x, y ->
            // Line 0 has text; lines 1 and 2 are only spaces.
            y != 0 || x > 4
        }
        assertEquals(0, found)
    }
}
