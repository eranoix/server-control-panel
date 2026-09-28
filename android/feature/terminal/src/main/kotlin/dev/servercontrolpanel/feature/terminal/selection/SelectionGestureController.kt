package dev.servercontrolpanel.feature.terminal.selection

import androidx.compose.foundation.gestures.detectDragGesturesAfterLongPress
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.pointer.pointerInput
import dev.servercontrolpanel.feature.terminal.mouse.TouchRouting

enum class DragPhase { START, MOVE, END }

fun interface CanvasDragTarget {
    fun onDrag(position: Offset, phase: DragPhase)
}

class SelectionGestureController(
    private val cellHitTester: () -> CellHitTester,
    private val selectionHolder: GridSelectionHolder,
    private val onChange: (GridSelection?) -> Unit = {},
) : CanvasDragTarget {

    override fun onDrag(position: Offset, phase: DragPhase) {
        val cell = cellHitTester().hitTest(position)
        when (phase) {
            DragPhase.START -> setSelection(
                GridSelection(startRow = cell.row, startCol = cell.col, endRow = cell.row, endCol = cell.col),
            )
            DragPhase.MOVE -> {
                val current = selectionHolder.selection ?: return
                setSelection(current.copy(endRow = cell.row, endCol = cell.col))
            }
            DragPhase.END -> onChange(selectionHolder.selection)
        }
    }

    fun setSelection(selection: GridSelection?) {
        selectionHolder.selection = selection
        onChange(selection)
    }

    fun dragHandle(handle: SelectionHandle, position: Offset) {
        val current = selectionHolder.selection ?: return
        val cell = cellHitTester().hitTest(position)
        val next = when (handle) {
            SelectionHandle.START -> current.copy(startRow = cell.row, startCol = cell.col)
            SelectionHandle.END -> current.copy(endRow = cell.row, endCol = cell.col)
        }
        setSelection(next)
    }

    fun clearSelection() {
        if (selectionHolder.selection == null) return
        setSelection(null)
    }
}

fun routeCanvasDrag(
    routing: TouchRouting,
    selectionTarget: CanvasDragTarget,
    mouseTarget: CanvasDragTarget,
): CanvasDragTarget = CanvasDragTarget { position, phase ->
    val target = if (routing.tapBelongsToApp()) selectionTarget else mouseTarget
    target.onDrag(position, phase)
}

fun Modifier.canvasDragGestures(target: CanvasDragTarget): Modifier = pointerInput(target) {
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
