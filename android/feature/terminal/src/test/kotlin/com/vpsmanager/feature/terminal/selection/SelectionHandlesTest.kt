package com.vpsmanager.feature.terminal.selection

import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * Draggable selection handles, as in Android text fields. All geometry comes from the
 * same [CellHitTester] that maps touches to cells, so there is one rounding convention.
 */
class SelectionHandlesTest {

    private val hitTester = CellHitTester(cellWidthPx = 20f, cellHeightPx = 40f, cols = 10, rows = 5)

    @Test
    fun anchors_hangFromBottomEdgesOfFirstAndLastCell() {
        // Android convention: the start handle hangs from the bottom-left of the first
        // cell, the end handle from the bottom-right of the last.
        val selection = GridSelection(startRow = 1, startCol = 2, endRow = 1, endCol = 4)

        val anchors = handleAnchors(selection, hitTester)

        assertEquals(Offset(40f, 80f), anchors.start)
        assertEquals(Offset(100f, 80f), anchors.end)
    }

    @Test
    fun anchors_doNotSwapSidesWhenDraggedBackToFront() {
        // An inverted (right-to-left) selection must not make the handles swap sides
        // mid-gesture.
        val forward = GridSelection(startRow = 0, startCol = 1, endRow = 0, endCol = 5)
        val backward = GridSelection(startRow = 0, startCol = 5, endRow = 0, endCol = 1)

        assertEquals(handleAnchors(forward, hitTester), handleAnchors(backward, hitTester))
    }

    @Test
    fun tapOnHandleHitsHandle() {
        val selection = GridSelection(0, 2, 2, 6)
        val anchors = handleAnchors(selection, hitTester)

        assertEquals(SelectionHandle.START, handleAt(anchors.start, anchors, radiusPx = 48f))
        assertEquals(SelectionHandle.END, handleAt(anchors.end, anchors, radiusPx = 48f))
    }

    @Test
    fun tapFarFromBoth_hitsNoHandle_andGestureGoesBackToGrid() {
        val selection = GridSelection(0, 2, 0, 4)
        val anchors = handleAnchors(selection, hitTester)

        assertNull(handleAt(Offset(180f, 180f), anchors, radiusPx = 48f))
    }

    @Test
    fun withBothInReach_nearestWins() {
        // Single-cell selection: both anchors are 20 px apart, inside one 48 dp target.
        val selection = GridSelection(0, 0, 0, 0)
        val anchors = handleAnchors(selection, hitTester)

        assertEquals(SelectionHandle.START, handleAt(Offset(2f, 40f), anchors, radiusPx = 48f))
        assertEquals(SelectionHandle.END, handleAt(Offset(19f, 40f), anchors, radiusPx = 48f))
    }

    @Test
    fun draggingEndHandle_movesOnlyTheEnd_leavingStartInPlace() {
        val holder = GridSelectionHolder()
        val controller = SelectionGestureController({ hitTester }, holder)
        controller.setSelection(GridSelection(0, 1, 0, 3))

        // Drop the end on the cell (row 2, column 7).
        controller.dragHandle(SelectionHandle.END, Offset(150f, 100f))

        assertEquals(GridSelection(0, 1, 2, 7), holder.selection)
    }

    @Test
    fun draggingStartHandle_movesOnlyTheStart() {
        val holder = GridSelectionHolder()
        val controller = SelectionGestureController({ hitTester }, holder)
        controller.setSelection(GridSelection(1, 4, 3, 8))

        controller.dragHandle(SelectionHandle.START, Offset(10f, 10f))

        assertEquals(GridSelection(0, 0, 3, 8), holder.selection)
    }

    @Test
    fun handlesMayCross_andTextComesOutInReadingOrder() {
        val holder = GridSelectionHolder()
        val controller = SelectionGestureController({ hitTester }, holder)
        controller.setSelection(GridSelection(0, 2, 0, 5))

        // Dragging the start handle past the end is a legitimate gesture.
        controller.dragHandle(SelectionHandle.START, Offset(170f, 10f))

        val crossed = holder.selection!!
        assertEquals(GridSelection(0, 8, 0, 5), crossed)
        // Crossed in the data, but ordered for measuring and drawing.
        assertEquals(Rect(100f, 0f, 180f, 40f), selectionBounds(crossed, hitTester))
    }

    @Test
    fun draggingHandleWithoutSelection_doesNotCreateOne() {
        val holder = GridSelectionHolder()
        val controller = SelectionGestureController({ hitTester }, holder)

        controller.dragHandle(SelectionHandle.END, Offset(50f, 50f))

        assertNull(holder.selection)
    }

    @Test
    fun selectionRectIsWhatFloatingBarUsesToPosition() {
        // Without this rect the system would position the bar using the whole view.
        val selection = GridSelection(startRow = 1, startCol = 2, endRow = 1, endCol = 4)

        assertEquals(Rect(40f, 40f, 100f, 80f), selectionBounds(selection, hitTester))
    }

    @Test
    fun multiRowHighlight_coversMiddleRowsFully() {
        val bands = selectionRowRanges(GridSelection(0, 7, 2, 3), cols = 10)

        assertEquals(listOf(7..9, 0..9, 0..3), bands)
    }

    @Test
    fun singleRowHighlight_staysBetweenBothColumns() {
        assertEquals(listOf(2..6), selectionRowRanges(GridSelection(4, 2, 4, 6), cols = 10))
    }

    @Test
    fun selectionChangeNotifiesBarDrawer() {
        // The system floating bar does not observe the holder, so it must be notified.
        val notices = mutableListOf<GridSelection?>()
        val holder = GridSelectionHolder()
        val controller = SelectionGestureController({ hitTester }, holder) { notices += it }

        controller.setSelection(GridSelection(0, 0, 0, 2))
        controller.dragHandle(SelectionHandle.END, Offset(90f, 10f))
        controller.clearSelection()

        assertEquals(3, notices.size)
        assertNull("the last notice says there is no selection anymore", notices.last())
    }

    @Test
    fun clearingMissingSelection_doesNotNotifyAgain() {
        // Closing the bar clears the selection and vice versa; this guard prevents a loop.
        val notices = mutableListOf<GridSelection?>()
        val holder = GridSelectionHolder()
        val controller = SelectionGestureController({ hitTester }, holder) { notices += it }

        controller.clearSelection()

        assertEquals(0, notices.size)
    }
}
