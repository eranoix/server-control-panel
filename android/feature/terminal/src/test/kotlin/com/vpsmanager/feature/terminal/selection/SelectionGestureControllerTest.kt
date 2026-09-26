package com.vpsmanager.feature.terminal.selection

import androidx.compose.ui.geometry.Offset
import com.vpsmanager.feature.terminal.mouse.RoteamentoDeToque
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Host-runnable: exercises [SelectionGestureController] as a scripted drag
 * path (start cell, then a sequence of moves), asserting every
 * [GridSelection] value written matches exactly what [CellHitTester] would
 * report for that pixel — never a value derived from raw pixel deltas.
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
    fun routeCanvasDrag_programaNaoPediuMouse_soASelecaoRecebeOgesto() {
        // The `bash` prompt — no tracking active. Previously a manual switch
        // could send the gesture to the "mouse" even here, and the bytes ended
        // up as text on the command line.
        val toggle = RoteamentoDeToque { false }
        val selectionCalls = mutableListOf<Offset>()
        val mouseCalls = mutableListOf<Offset>()
        val selectionTarget = CanvasDragTarget { position, _ -> selectionCalls += position }
        val mouseTarget = CanvasDragTarget { position, _ -> mouseCalls += position }

        val router = routeCanvasDrag(toggle, selectionTarget, mouseTarget)
        router.onDrag(Offset(1f, 2f), DragPhase.START)

        assertEquals(listOf(Offset(1f, 2f)), selectionCalls)
        assertTrue("sem programa pedindo mouse, nenhum byte de mouse pode ser gerado", mouseCalls.isEmpty())
    }

    @Test
    fun routeCanvasDrag_programaPediuMouse_soOmouseRecebeOgesto() {
        val toggle = RoteamentoDeToque { true }
        val selectionCalls = mutableListOf<Offset>()
        val mouseCalls = mutableListOf<Offset>()
        val selectionTarget = CanvasDragTarget { position, _ -> selectionCalls += position }
        val mouseTarget = CanvasDragTarget { position, _ -> mouseCalls += position }

        val router = routeCanvasDrag(toggle, selectionTarget, mouseTarget)
        router.onDrag(Offset(3f, 4f), DragPhase.START)

        assertEquals(listOf(Offset(3f, 4f)), mouseCalls)
        assertTrue("com o programa pedindo mouse, a seleção não recebe o arraste", selectionCalls.isEmpty())
    }

    @Test
    fun routeCanvasDrag_seguirOprogramaRemotoNaoEmaisNegociavel() {
        // There used to be a test here for the "the program asked for the
        // mouse but I want to select anyway" preference. The preference was
        // REMOVED at the app owner's request, and this test now pins that
        // removal down: with the program asking for the mouse, the drag is
        // its own — no state in the app can divert that any more.
        //
        // Selecting inside an `htop` is still possible through a LONG PRESS,
        // which anchors the selection before this routing and therefore does
        // not show up in this test.
        val roteamento = RoteamentoDeToque { true }
        val selectionCalls = mutableListOf<Offset>()
        val mouseCalls = mutableListOf<Offset>()
        val selectionTarget = CanvasDragTarget { position, _ -> selectionCalls += position }
        val mouseTarget = CanvasDragTarget { position, _ -> mouseCalls += position }

        val router = routeCanvasDrag(roteamento, selectionTarget, mouseTarget)
        router.onDrag(Offset(5f, 6f), DragPhase.START)

        assertEquals(listOf(Offset(5f, 6f)), mouseCalls)
        assertTrue("nenhum estado do app desvia o gesto do programa", selectionCalls.isEmpty())
    }
}
