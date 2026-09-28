package dev.servercontrolpanel.feature.terminal.selection

import androidx.compose.ui.geometry.Offset
import org.junit.Assert.assertEquals
import org.junit.Test

class CellHitTesterTest {

    private val tester = CellHitTester(cellWidthPx = 20f, cellHeightPx = 30f, cols = 10, rows = 8)

    @Test
    fun cellCenters_mapToExpectedCell() {
        assertEquals(CellHitTester.Cell(row = 2, col = 3), tester.hitTest(Offset(70f, 75f)))
        assertEquals(CellHitTester.Cell(row = 0, col = 0), tester.hitTest(Offset(10f, 15f)))
    }

    @Test
    fun cellEdges_floorToTheCellStartingAtThatEdge() {
        assertEquals(3, tester.hitTest(Offset(60f, 0f)).col)
        assertEquals(3, tester.hitTest(Offset(0f, 90f)).row)
        assertEquals(2, tester.hitTest(Offset(59.999f, 0f)).col)
        assertEquals(2, tester.hitTest(Offset(0f, 89.999f)).row)
    }

    @Test
    fun pointsBetweenCells_mapToTheEnclosingCell() {
        assertEquals(3, tester.hitTest(Offset(61f, 0f)).col)
        assertEquals(3, tester.hitTest(Offset(79f, 0f)).col)
    }

    @Test
    fun tapAboveLeftOfOrigin_clampsToFirstCell_neverNegative() {
        assertEquals(CellHitTester.Cell(row = 0, col = 0), tester.hitTest(Offset(-50f, -50f)))
        assertEquals(CellHitTester.Cell(row = 0, col = 0), tester.hitTest(Offset(-1f, -1f)))
    }

    @Test
    fun tapBeyondLastRowOrColumn_clampsToLastValidCell() {
        assertEquals(CellHitTester.Cell(row = 7, col = 9), tester.hitTest(Offset(5000f, 5000f)))
        assertEquals(9, tester.hitTest(Offset(199.999f, 0f)).col)
        assertEquals(9, tester.hitTest(Offset(200f, 0f)).col)
    }

    @Test
    fun cellRect_roundTripsThroughHitTest_forEveryCellIncludingEdges() {
        val cellsToCheck = listOf(0 to 0, 3 to 2, 9 to 7, 0 to 7, 9 to 0)
        for ((col, row) in cellsToCheck) {
            val rect = tester.cellRect(row = row, col = col)
            val center = rect.center
            assertEquals(CellHitTester.Cell(row = row, col = col), tester.hitTest(center))
        }
    }

    @Test
    fun cellRect_withNonZeroOrigin_stillRoundTrips() {
        val offsetTester = CellHitTester(
            cellWidthPx = 16f,
            cellHeightPx = 28f,
            cols = 5,
            rows = 5,
            originX = 12f,
            originY = 40f,
        )
        val rect = offsetTester.cellRect(row = 2, col = 3)
        assertEquals(CellHitTester.Cell(row = 2, col = 3), offsetTester.hitTest(rect.center))
        assertEquals(CellHitTester.Cell(row = 0, col = 0), offsetTester.hitTest(Offset(0f, 0f)))
    }
}
