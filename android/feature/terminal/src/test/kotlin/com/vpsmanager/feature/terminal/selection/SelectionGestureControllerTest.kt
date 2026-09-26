package com.vpsmanager.feature.terminal.selection

import androidx.compose.ui.geometry.Offset
import com.vpsmanager.feature.terminal.mouse.TouchRouting
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Drives [SelectionGestureController] through scripted drags and checks that every
 * [GridSelection] matches what [CellHitTester] reports for that pixel, never a value
 * derived from raw pixel deltas.
 */
class SelectionGestureControllerTest {

    private val hitTester = CellHitTester(cellWidthPx = 20f, cellHeightPx = 30f, cols = 20, rows = 20)
    private val holder = GridSelectionHolder()
    private val controller = SelectionGestureController({ hitTester }, holder)

    @Test
    fun longPressStart_anchorsSelectionAtHitTestedCell() {
        controller.onDrag(Offset(45f, 65f), DragPhase.START)

        val expectedCell = hitTester.hitTest(Offset(45f, 65f))
        assertEquals(
            GridSelection(startRow = expectedCell.row, startCol = expectedCell.col, endRow = expectedCell.row, endCol = expectedCell.col),
            holder.selection,
        )
    }

    @Test
    fun drag_updatesEndCellLive_keepingStartCellFixed() {
        controller.onDrag(Offset(10f, 10f), DragPhase.START)
        val startCell = hitTester.hitTest(Offset(10f, 10f))

        controller.onDrag(Offset(90f, 130f), DragPhase.MOVE)
        val midCell = hitTester.hitTest(Offset(90f, 130f))
        assertEquals(
            GridSelection(startRow = startCell.row, startCol = startCell.col, endRow = midCell.row, endCol = midCell.col),
            holder.selection,
        )

        controller.onDrag(Offset(150f, 200f), DragPhase.MOVE)
        val endCell = hitTester.hitTest(Offset(150f, 200f))
        assertEquals(
            GridSelection(startRow = startCell.row, startCol = startCell.col, endRow = endCell.row, endCol = endCell.col),
            holder.selection,
        )
    }

    @Test
    fun dragBeforeStart_isANoOp_neverCreatesASelectionOutOfThinAir() {
        controller.onDrag(Offset(50f, 50f), DragPhase.MOVE)
        assertNull(holder.selection)
    }

    @Test
    fun dragEnd_leavesTheFinalSelectionUntouched() {
        controller.onDrag(Offset(10f, 10f), DragPhase.START)
        controller.onDrag(Offset(90f, 90f), DragPhase.MOVE)
        val beforeEnd = holder.selection

        controller.onDrag(Offset(0f, 0f), DragPhase.END)

        assertEquals(beforeEnd, holder.selection)
    }

    @Test
    fun clearSelection_discardsTheCurrentSelection() {
        controller.onDrag(Offset(10f, 10f), DragPhase.START)
        assertTrue(holder.selection != null)

        controller.clearSelection()

        assertNull(holder.selection)
    }

    @Test
    fun routeCanvasDrag_programDidNotAskForMouse_onlySelectionGetsGesture() {
        // A `bash` prompt with no mouse tracking: mouse bytes would land as text.
        val toggle = TouchRouting { false }
        val selectionCalls = mutableListOf<Offset>()
        val mouseCalls = mutableListOf<Offset>()
        val selectionTarget = CanvasDragTarget { position, _ -> selectionCalls += position }
        val mouseTarget = CanvasDragTarget { position, _ -> mouseCalls += position }

        val router = routeCanvasDrag(toggle, selectionTarget, mouseTarget)
        router.onDrag(Offset(1f, 2f), DragPhase.START)

        assertEquals(listOf(Offset(1f, 2f)), selectionCalls)
        assertTrue("with no program asking for the mouse, no mouse bytes may be generated", mouseCalls.isEmpty())
    }

    @Test
    fun routeCanvasDrag_programAskedForMouse_onlyMouseGetsGesture() {
        val toggle = TouchRouting { true }
        val selectionCalls = mutableListOf<Offset>()
        val mouseCalls = mutableListOf<Offset>()
        val selectionTarget = CanvasDragTarget { position, _ -> selectionCalls += position }
        val mouseTarget = CanvasDragTarget { position, _ -> mouseCalls += position }

        val router = routeCanvasDrag(toggle, selectionTarget, mouseTarget)
        router.onDrag(Offset(3f, 4f), DragPhase.START)

        assertEquals(listOf(Offset(3f, 4f)), mouseCalls)
        assertTrue("with the program asking for the mouse, selection does not get the drag", selectionCalls.isEmpty())
    }

    @Test
    fun routeCanvasDrag_followingRemoteProgramIsNotNegotiable() {
        // When the program asks for the mouse, the drag is always its own. Selecting
        // inside e.g. `htop` still works through a long press, which is routed earlier.
        val routing = TouchRouting { true }
        val selectionCalls = mutableListOf<Offset>()
        val mouseCalls = mutableListOf<Offset>()
        val selectionTarget = CanvasDragTarget { position, _ -> selectionCalls += position }
        val mouseTarget = CanvasDragTarget { position, _ -> mouseCalls += position }

        val router = routeCanvasDrag(routing, selectionTarget, mouseTarget)
        router.onDrag(Offset(5f, 6f), DragPhase.START)

        assertEquals(listOf(Offset(5f, 6f)), mouseCalls)
        assertTrue("no app state diverts the gesture away from the program", selectionCalls.isEmpty())
    }
}
