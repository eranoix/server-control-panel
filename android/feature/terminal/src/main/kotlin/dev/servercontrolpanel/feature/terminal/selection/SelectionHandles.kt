package dev.servercontrolpanel.feature.terminal.selection

import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect

/** Which of the selection's two ends a handle controls. */
enum class SelectionHandle { START, END }

/**
 * Where the two handles are anchored, in grid pixels.
 *
 * The convention is Android's: the start handle hangs from the **bottom left**
 * edge of the first selected cell and the end handle from the **bottom right**
 * of the last — which is why, in a text field, the left handle points up and
 * to the right and the right one up and to the left: each "points at" the
 * character it delimits.
 */
data class HandleAnchors(val start: Offset, val end: Offset)

/**
 * Translates a selection in cell coordinates into the two anchor points of the
 * handles, using the SAME [CellHitTester] that maps a touch to a cell — one
 * rounding convention across the whole gesture, in both directions.
 *
 * The selection is normalised into reading order first: dragging from bottom
 * to top must not change which handle is "the start one" on screen, or the
 * handle runs away from the finger mid-drag.
 */
fun handleAnchors(selection: GridSelection, hitTester: CellHitTester): HandleAnchors {
    val ordered = inReadingOrder(selection)
    val first = hitTester.cellRect(ordered.startRow, ordered.startCol)
    val last = hitTester.cellRect(ordered.endRow, ordered.endCol)
    return HandleAnchors(
        start = Offset(first.left, first.bottom),
        end = Offset(last.right, last.bottom),
    )
}

/**
 * Which handle the finger caught, or `null` if it caught the grid.
 *
 * [radiusPx] is generous on purpose: the drawn handle is about 24 dp, but an
 * Android control's touch target is 48 dp, and a handle that only responds
 * exactly on its drawing is a handle that "does not work" in practice. When
 * both are within reach — a single-cell selection — the nearest one wins, and
 * on a tie the END one, which is the one dragged in the overwhelming majority
 * of adjustments.
 */
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

/**
 * The rectangle the selection occupies on screen — this is what the system's
 * floating bar receives in `onGetContentRect` so it can position itself ABOVE
 * the selected text instead of on top of it.
 */
fun selectionBounds(selection: GridSelection, hitTester: CellHitTester): Rect {
    val ordered = inReadingOrder(selection)
    val first = hitTester.cellRect(ordered.startRow, ordered.startCol)
    val last = hitTester.cellRect(ordered.endRow, ordered.endCol)
    // On a multi-line selection the rectangle is the whole band: the bar needs
    // to know the content is tall, not just where the first cell is.
    val left = if (ordered.startRow == ordered.endRow) first.left else minOf(first.left, last.left)
    val right = if (ordered.startRow == ordered.endRow) last.right else maxOf(first.right, last.right)
    return Rect(left = left, top = first.top, right = right, bottom = last.bottom)
}

/**
 * The runs of cells the highlight covers, one per line of the selection — the
 * first starts at the start column, the last ends at the end column, and the
 * ones in between take the whole line. It is the same shape any multi-line
 * text selection on Android has.
 */
fun selectionRowRanges(selection: GridSelection, cols: Int): List<IntRange> {
    val ordered = inReadingOrder(selection)
    return (ordered.startRow..ordered.endRow).map { row ->
        val from = if (row == ordered.startRow) ordered.startCol else 0
        val until = if (row == ordered.endRow) ordered.endCol else cols - 1
        from..until
    }
}

/**
 * Puts the selection into reading order (start before end). The handles CAN
 * cross during the drag — that is a legitimate gesture — and this is where
 * that stops mattering to anyone who just wants to draw or measure.
 */
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
