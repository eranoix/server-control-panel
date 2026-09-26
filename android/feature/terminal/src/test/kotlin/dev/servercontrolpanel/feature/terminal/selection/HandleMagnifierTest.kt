package dev.servercontrolpanel.feature.terminal.selection

import androidx.compose.ui.geometry.Offset
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * The magnifier's Y must stick to the center of the line, not to the finger, so it
 * does not tremble with the hand or show half of two lines.
 */
class HandleMagnifierTest {

    private val hitTester = CellHitTester(
        cellWidthPx = 10f,
        cellHeightPx = 20f,
        cols = 80,
        rows = 24,
    )

    @Test
    fun `the Y goes to the center of the cell, not to the finger`() {
        // Finger at y=25: inside row 1 (20..40), but near the top of it.
        val cell = hitTester.hitTest(Offset(x = 35f, y = 25f))
        val rect = hitTester.cellRect(cell.row, cell.col)

        assertEquals("row under the finger", 1, cell.row)
        assertEquals("center of the row, not the finger's y", 30f, rect.center.y, 0.01f)
    }

    @Test
    fun `a finger wobbling inside the same row does not move the magnifier`() {
        val high = hitTester.hitTest(Offset(x = 35f, y = 21f))
        val down = hitTester.hitTest(Offset(x = 35f, y = 39f))

        val yHigh = hitTester.cellRect(high.row, high.col).center.y
        val yDown = hitTester.cellRect(down.row, down.col).center.y

        assertEquals("18 px of tremor in the same row means zero magnifier movement", yHigh, yDown, 0.01f)
    }

    @Test
    fun `changing rows moves the magnifier by a whole row`() {
        val first = hitTester.hitTest(Offset(x = 35f, y = 25f))
        val second = hitTester.hitTest(Offset(x = 35f, y = 45f))

        val y1 = hitTester.cellRect(first.row, first.col).center.y
        val y2 = hitTester.cellRect(second.row, second.col).center.y

        assertEquals("exactly one cell height", 20f, y2 - y1, 0.01f)
    }
}
