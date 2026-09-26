package com.vpsmanager.feature.terminal.selection

import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * The draggable handles — what the terminal's selection was missing to feel
 * like that of an Android text field.
 *
 * Before there was only one gesture: long press, drag, release. Missed the end
 * by one cell? Start over from scratch. With handles, adjusting means grabbing
 * the wrong end and moving it — and the text that gets copied is the one
 * highlighted, not the one you remember having dragged.
 *
 * All the geometry comes from the SAME [CellHitTester] that maps a touch onto
 * a cell: one rounding convention only, there and back.
 */
class SelectionHandlesTest {

    private val hitTester = CellHitTester(cellWidthPx = 20f, cellHeightPx = 40f, cols = 10, rows = 5)

    @Test
    fun anchors_hangFromBottomEdgesOfFirstAndLastCell() {
        // The Android convention: the left handle hangs from the bottom LEFT
        // edge of the first character, the right one from the bottom RIGHT of
        // the last — that is what makes each "point at" what it delimits.
        val selection = GridSelection(startRow = 1, startCol = 2, endRow = 1, endCol = 4)

        val anchors = handleAnchors(selection, hitTester)

        assertEquals(Offset(40f, 80f), anchors.start)
        assertEquals(Offset(100f, 80f), anchors.end)
    }

    @Test
    fun anchors_doNotSwapSidesWhenDraggedBackToFront() {
        // Dragging right to left produces an "inverted" selection. If the
        // start handle jumped to the other side mid-gesture, it would run away
        // from the finger.
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
        // A single-cell selection: the two anchors sit 20 px apart, inside
        // the same 48 dp target.
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

        // Drag the START handle past the end — a legitimate gesture in any
        // Android text field.
        controller.dragHandle(SelectionHandle.START, Offset(170f, 10f))

        val crossed = holder.selection!!
        assertEquals(GridSelection(0, 8, 0, 5), crossed)
        // Crossed in the data, ordered when it comes to measuring and drawing.
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
        // Without this rectangle the system would use the bounds of the whole
        // view and the bar would land at the top of the screen, far from what
        // was selected.
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
        // The floating bar belongs to the system: nothing observes the holder
        // on its own, it has to be CALLED. Without this notice the selection
        // would exist with no bar.
        val notices = mutableListOf<GridSelection?>()
        val holder = GridSelectionHolder()
        val controller = SelectionGestureController({ hitTester }, holder) { notices += it }

        controller.setSelection(GridSelection(0, 0, 0, 2))
        controller.dragHandle(SelectionHandle.END, Offset(90f, 10f))
        controller.clearSelection()

        assertEquals(3, notices.size)
        assertNull("o último aviso é o de que não há mais seleção", notices.last())
    }

    @Test
    fun clearingMissingSelection_doesNotNotifyAgain() {
        // Closing the bar clears the selection, and clearing the selection
        // closes the bar. Without this guard the pair would loop.
        val notices = mutableListOf<GridSelection?>()
        val holder = GridSelectionHolder()
        val controller = SelectionGestureController({ hitTester }, holder) { notices += it }

        controller.clearSelection()

        assertEquals(0, notices.size)
    }
}
