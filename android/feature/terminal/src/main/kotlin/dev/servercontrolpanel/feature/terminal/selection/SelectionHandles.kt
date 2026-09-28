package dev.servercontrolpanel.feature.terminal.selection

import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect

enum class SelectionHandle { START, END }

data class HandleAnchors(val start: Offset, val end: Offset)

fun handleAnchors(selection: GridSelection, hitTester: CellHitTester): HandleAnchors {
    val ordered = inReadingOrder(selection)
    val first = hitTester.cellRect(ordered.startRow, ordered.startCol)
    val last = hitTester.cellRect(ordered.endRow, ordered.endCol)
    return HandleAnchors(
        start = Offset(first.left, first.bottom),
        end = Offset(last.right, last.bottom),
    )
}

fun handleAt(position: Offset, anchors: HandleAnchors, radiusPx: Float): SelectionHandle? {
    val startDistance = (position - anchors.start).getDistance()
    val endDistance = (position - anchors.end).getDistance()
    val startInReach = startDistance <= radiusPx
    val endInReach = endDistance <= radiusPx
    return when {
        startInReach && endInReach -> if (startDistance < endDistance) SelectionHandle.START else SelectionHandle.END
        startInReach -> SelectionHandle.START
        endInReach -> SelectionHandle.END
        else -> null
    }
}

fun selectionBounds(selection: GridSelection, hitTester: CellHitTester): Rect {
    val ordered = inReadingOrder(selection)
    val first = hitTester.cellRect(ordered.startRow, ordered.startCol)
    val last = hitTester.cellRect(ordered.endRow, ordered.endCol)
    val left = if (ordered.startRow == ordered.endRow) first.left else minOf(first.left, last.left)
    val right = if (ordered.startRow == ordered.endRow) last.right else maxOf(first.right, last.right)
    return Rect(left = left, top = first.top, right = right, bottom = last.bottom)
}

fun selectionRowRanges(selection: GridSelection, cols: Int): List<IntRange> {
    val ordered = inReadingOrder(selection)
    return (ordered.startRow..ordered.endRow).map { row ->
        val from = if (row == ordered.startRow) ordered.startCol else 0
        val until = if (row == ordered.endRow) ordered.endCol else cols - 1
        from..until
    }
}

internal fun inReadingOrder(selection: GridSelection): GridSelection {
    val pastEnd = selection.startRow > selection.endRow ||
        (selection.startRow == selection.endRow && selection.startCol > selection.endCol)
    return if (pastEnd) {
        GridSelection(
            startRow = selection.endRow,
            startCol = selection.endCol,
            endRow = selection.startRow,
            endCol = selection.startCol,
        )
    } else {
        selection
    }
}
