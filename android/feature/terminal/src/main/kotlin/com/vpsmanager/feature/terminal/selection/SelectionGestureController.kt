package com.vpsmanager.feature.terminal.selection

import androidx.compose.foundation.gestures.detectDragGesturesAfterLongPress
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.pointer.pointerInput
import com.vpsmanager.feature.terminal.mouse.RoteamentoDeToque

/** The phase of a canvas drag gesture, as delivered to a [CanvasDragTarget]. */
enum class DragPhase { START, MOVE, END }

/**
 * A canvas drag gesture's exclusive destination — [SelectionGestureController]
 * (this file) or the mouse-report byte encoder
 * ([com.vpsmanager.feature.terminal.mouse.MouseReportGestureController]).
 * [routeCanvasDrag] is what enforces "exclusive": exactly one of the two ever
 * receives a given gesture's callbacks, decided by
 * [com.vpsmanager.feature.terminal.mouse.RoteamentoDeToque] — today from the
 * terminal's REAL state, not from a manual switch.
 */
fun interface CanvasDragTarget {
    fun onDrag(position: Offset, phase: DragPhase)
}

/**
 * Drives [GridSelectionHolder] from real long-press-and-drag touch gestures,
 * via [CellHitTester] — cell coordinates only, never raw pixel deltas and
 * never anything the render pass produces.
 *
 * Deliberately has zero reference to `TerminalInputConnection` or any IME/
 * composition type — see `TerminalInputConnectionTest` and
 * `SelectionComposeIndependenceTest` for the independence proof this
 * controller must uphold under real, continuous touch input.
 *
 * [cellHitTester] is a supplier rather than a fixed value because the
 * caller ([com.vpsmanager.feature.terminal.ui.TerminalRoute]) rebuilds the
 * tester whenever layout/font-size changes; this controller always reads
 * the current one, never a stale snapshot from construction time.
 *
 * [aoMudar] is how the system's floating bar learns that there is (or has
 * stopped being) a selection: the bar is drawn by Android, and it has to be
 * called — nothing here observes this holder on its own.
 */
class SelectionGestureController(
    private val cellHitTester: () -> CellHitTester,
    private val selectionHolder: GridSelectionHolder,
    private val aoMudar: (GridSelection?) -> Unit = {},
) : CanvasDragTarget {

    override fun onDrag(position: Offset, phase: DragPhase) {
        val cell = cellHitTester().hitTest(position)
        when (phase) {
            DragPhase.START -> definirSelecao(
                GridSelection(startRow = cell.row, startCol = cell.col, endRow = cell.row, endCol = cell.col),
            )
            DragPhase.MOVE -> {
                val current = selectionHolder.selection ?: return
                definirSelecao(current.copy(endRow = cell.row, endCol = cell.col))
            }
            DragPhase.END -> aoMudar(selectionHolder.selection)
        }
    }

    /**
     * Replaces the whole selection in one go — the path taken by double-tap
     * (word) and triple-tap (line), and by "Select all" on the system bar.
     */
    fun definirSelecao(selection: GridSelection?) {
        selectionHolder.selection = selection
        aoMudar(selection)
    }

    /**
     * Drags one of the handles to the cell under [position].
     *
     * The handles may CROSS — dragging the start one past the end is a
     * legitimate gesture in any Android text field, and the resulting
     * selection stays valid because the reading order is normalised when the
     * text is extracted ([extractSelectedText]), not here.
     */
    fun arrastarAlca(alca: SelectionHandle, position: Offset) {
        val atual = selectionHolder.selection ?: return
        val cell = cellHitTester().hitTest(position)
        val nova = when (alca) {
            SelectionHandle.INICIO -> atual.copy(startRow = cell.row, startCol = cell.col)
            SelectionHandle.FIM -> atual.copy(endRow = cell.row, endCol = cell.col)
        }
        definirSelecao(nova)
    }

    /** Discards the current selection (e.g. after copy, or a tap outside it). */
    fun clearSelection() {
        if (selectionHolder.selection == null) return
        definirSelecao(null)
    }
}

/**
 * Routes a canvas drag gesture to exactly one of [selectionTarget] or
 * [mouseTarget], decided by [roteamento] at the moment this returned
 * [CanvasDragTarget] is invoked — selection and mouse-reporting are mutually
 * exclusive per gesture, never both.
 *
 * What differs from the previous version is the SOURCE of the criterion: it
 * used to be a manual switch in the app, and today it is the mode the remote
 * program turned on in the terminal (see [RoteamentoDeToque]) — with no
 * operator preference layered over it.
 */
fun routeCanvasDrag(
    roteamento: RoteamentoDeToque,
    selectionTarget: CanvasDragTarget,
    mouseTarget: CanvasDragTarget,
): CanvasDragTarget = CanvasDragTarget { position, phase ->
    val target = if (roteamento.toqueEDoApp()) selectionTarget else mouseTarget
    target.onDrag(position, phase)
}

/**
 * Attaches the long-press-then-drag gesture detector that drives [target]
 * ([SelectionGestureController] when selection-priority, the mouse-report
 * controller otherwise via [routeCanvasDrag]) — long-press anchors the
 * gesture's start cell, drag updates its end cell live, matching Android's
 * native text-selection feel without an `EditText` anywhere in the chain.
 */
fun Modifier.canvasDragGestures(target: CanvasDragTarget): Modifier = pointerInput(target) {
    // onDragEnd/onDragCancel carry no position of their own (Compose's
    // gesture API), so the last real touch point is tracked locally and
    // replayed as the END phase's position -- important for mouse-report
    // release events, which must land at the last dragged-to cell, never
    // back at the origin.
    var lastPosition = Offset.Zero
    detectDragGesturesAfterLongPress(
        onDragStart = { offset ->
            lastPosition = offset
            target.onDrag(offset, DragPhase.START)
        },
        onDrag = { change, _ ->
            lastPosition = change.position
            target.onDrag(change.position, DragPhase.MOVE)
            change.consume()
        },
        onDragEnd = { target.onDrag(lastPosition, DragPhase.END) },
        onDragCancel = { target.onDrag(lastPosition, DragPhase.END) },
    )
}
