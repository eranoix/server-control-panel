package dev.servercontrolpanel.feature.terminal.selection

import dev.servercontrolpanel.terminalengine.CellSnapshot

/**
 * The character classes that define word boundaries, the classic terminal split
 * (xterm, Termux, iTerm): double-tapping `commit` in `git commit --amend` selects `commit`.
 */
private enum class CharClass { SPACE, WORD, OTHER }

/**
 * `_` is WORD because identifiers and variables use it mid-word. `-`, `.` and `/`
 * are not, so a double tap on a path selects one segment (triple tap takes the line).
 */
private fun classify(codepoint: Int): CharClass = when {
    // A never-written cell (same convention as `buildRowDrawOps`) counts as space.
    codepoint == 0 -> CharClass.SPACE
    Character.isWhitespace(codepoint) -> CharClass.SPACE
    codepoint == '_'.code -> CharClass.WORD
    Character.isLetterOrDigit(codepoint) -> CharClass.WORD
    else -> CharClass.OTHER
}

/**
 * The class of cell [col] on [row]. A wide character's tail (`SPACER_TAIL`)
 * belongs to the character on its left, so CJK words are not cut in half.
 */
private fun cellClass(snapshot: CellSnapshot, row: Int, col: Int): CharClass {
    val cell = snapshot.cellAt(col, row)
    if (cell.wide == CellSnapshot.Wide.SPACER_TAIL && col > 0) {
        return classify(snapshot.cellAt(col - 1, row).codepoint)
    }
    return classify(cell.codepoint)
}

/**
 * The word under the tapped cell (double tap). Extends both ways while the
 * character class stays the same; tapping a space selects the run of spaces, as
 * in xterm, so a slightly missed tap is not a dead gesture.
 */
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

/**
 * The logical line through [row] (triple tap). A wrapped line spans several
 * screen rows, and the soft-wrap marks ([CellSnapshot.isWrapped] /
 * [CellSnapshot.isWrapContinuation]) join them, as in [extractSelectedText], so a
 * path or URL is not cut in half. Ends at the last column with content.
 */
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

/** The whole grid (the floating toolbar's "Select all"). */
fun selectAll(snapshot: CellSnapshot): GridSelection = GridSelection(
    startRow = 0,
    startCol = 0,
    endRow = snapshot.rows - 1,
    endCol = snapshot.cols - 1,
)

/** Last written column of the row, or 0 on a blank row (an empty but valid selection). */
private fun lastColumnWithContent(snapshot: CellSnapshot, row: Int): Int {
    for (col in snapshot.cols - 1 downTo 0) {
        if (snapshot.cellAt(col, row).codepoint != 0) return col
    }
    return 0
}
