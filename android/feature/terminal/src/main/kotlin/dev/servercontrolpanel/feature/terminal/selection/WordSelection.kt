package dev.servercontrolpanel.feature.terminal.selection

import dev.servercontrolpanel.terminalengine.CellSnapshot

private enum class CharClass { SPACE, WORD, OTHER }

private fun classify(codepoint: Int): CharClass = when {
    codepoint == 0 -> CharClass.SPACE
    Character.isWhitespace(codepoint) -> CharClass.SPACE
    codepoint == '_'.code -> CharClass.WORD
    Character.isLetterOrDigit(codepoint) -> CharClass.WORD
    else -> CharClass.OTHER
}

private fun cellClass(snapshot: CellSnapshot, row: Int, col: Int): CharClass {
    val cell = snapshot.cellAt(col, row)
    if (cell.wide == CellSnapshot.Wide.SPACER_TAIL && col > 0) {
        return classify(snapshot.cellAt(col - 1, row).codepoint)
    }
    return classify(cell.codepoint)
}

fun selectWord(snapshot: CellSnapshot, row: Int, col: Int): GridSelection {
    require(row in 0 until snapshot.rows) { "row $row outside the grid of ${snapshot.rows}" }
    require(col in 0 until snapshot.cols) { "column $col outside the grid of ${snapshot.cols}" }

    val className = cellClass(snapshot, row, col)
    var start = col
    while (start > 0 && cellClass(snapshot, row, start - 1) == className) start--
    var end = col
    while (end < snapshot.cols - 1 && cellClass(snapshot, row, end + 1) == className) end++

    return GridSelection(startRow = row, startCol = start, endRow = row, endCol = end)
}

fun selectLine(snapshot: CellSnapshot, row: Int): GridSelection {
    require(row in 0 until snapshot.rows) { "row $row outside the grid of ${snapshot.rows}" }

    var first = row
    while (first > 0 && snapshot.isWrapContinuation(first)) first--
    var last = row
    while (last < snapshot.rows - 1 && snapshot.isWrapped(last)) last++

    return GridSelection(
        startRow = first,
        startCol = 0,
        endRow = last,
        endCol = lastColumnWithContent(snapshot, last),
    )
}

fun selectAll(snapshot: CellSnapshot): GridSelection = GridSelection(
    startRow = 0,
    startCol = 0,
    endRow = snapshot.rows - 1,
    endCol = snapshot.cols - 1,
)

private fun lastColumnWithContent(snapshot: CellSnapshot, row: Int): Int {
    for (col in snapshot.cols - 1 downTo 0) {
        if (snapshot.cellAt(col, row).codepoint != 0) return col
    }
    return 0
}
