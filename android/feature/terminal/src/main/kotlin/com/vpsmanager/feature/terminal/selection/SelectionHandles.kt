package com.vpsmanager.feature.terminal.selection

import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect

/** Which of the selection's two ends a handle controls. */
enum class SelectionHandle { INICIO, FIM }

/**
 * Where the two handles are anchored, in grid pixels.
 *
 * The convention is Android's: the start handle hangs from the **bottom left**
 * edge of the first selected cell and the end handle from the **bottom right**
 * of the last — which is why, in a text field, the left handle points up and
 * to the right and the right one up and to the left: each "points at" the
 * character it delimits.
 */
data class HandleAnchors(val inicio: Offset, val fim: Offset)

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
    val ordenada = emOrdemDeLeitura(selection)
    val primeira = hitTester.cellRect(ordenada.startRow, ordenada.startCol)
    val ultima = hitTester.cellRect(ordenada.endRow, ordenada.endCol)
    return HandleAnchors(
        inicio = Offset(primeira.left, primeira.bottom),
        fim = Offset(ultima.right, ultima.bottom),
    )
}

/**
 * Which handle the finger caught, or `null` if it caught the grid.
 *
 * [raioPx] is generous on purpose: the drawn handle is about 24 dp, but an
 * Android control's touch target is 48 dp, and a handle that only responds
 * exactly on its drawing is a handle that "does not work" in practice. When
 * both are within reach — a single-cell selection — the nearest one wins, and
 * on a tie the END one, which is the one dragged in the overwhelming majority
 * of adjustments.
 */
fun handleAt(position: Offset, anchors: HandleAnchors, raioPx: Float): SelectionHandle? {
    val distanciaInicio = (position - anchors.inicio).getDistance()
    val distanciaFim = (position - anchors.fim).getDistance()
    val inicioAoAlcance = distanciaInicio <= raioPx
    val fimAoAlcance = distanciaFim <= raioPx
    return when {
        inicioAoAlcance && fimAoAlcance -> if (distanciaInicio < distanciaFim) SelectionHandle.INICIO else SelectionHandle.FIM
        inicioAoAlcance -> SelectionHandle.INICIO
        fimAoAlcance -> SelectionHandle.FIM
        else -> null
    }
}

/**
 * The rectangle the selection occupies on screen — this is what the system's
 * floating bar receives in `onGetContentRect` so it can position itself ABOVE
 * the selected text instead of on top of it.
 */
fun selectionBounds(selection: GridSelection, hitTester: CellHitTester): Rect {
    val ordenada = emOrdemDeLeitura(selection)
    val primeira = hitTester.cellRect(ordenada.startRow, ordenada.startCol)
    val ultima = hitTester.cellRect(ordenada.endRow, ordenada.endCol)
    // On a multi-line selection the rectangle is the whole band: the bar needs
    // to know the content is tall, not just where the first cell is.
    val esquerda = if (ordenada.startRow == ordenada.endRow) primeira.left else minOf(primeira.left, ultima.left)
    val direita = if (ordenada.startRow == ordenada.endRow) ultima.right else maxOf(primeira.right, ultima.right)
    return Rect(left = esquerda, top = primeira.top, right = direita, bottom = ultima.bottom)
}

/**
 * The runs of cells the highlight covers, one per line of the selection — the
 * first starts at the start column, the last ends at the end column, and the
 * ones in between take the whole line. It is the same shape any multi-line
 * text selection on Android has.
 */
fun selectionRowRanges(selection: GridSelection, cols: Int): List<IntRange> {
    val ordenada = emOrdemDeLeitura(selection)
    return (ordenada.startRow..ordenada.endRow).map { row ->
        val de = if (row == ordenada.startRow) ordenada.startCol else 0
        val ate = if (row == ordenada.endRow) ordenada.endCol else cols - 1
        de..ate
    }
}

/**
 * Puts the selection into reading order (start before end). The handles CAN
 * cross during the drag — that is a legitimate gesture — and this is where
 * that stops mattering to anyone who just wants to draw or measure.
 */
internal fun emOrdemDeLeitura(selection: GridSelection): GridSelection {
    val depoisDoFim = selection.startRow > selection.endRow ||
        (selection.startRow == selection.endRow && selection.startCol > selection.endCol)
    return if (depoisDoFim) {
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
