package com.vpsmanager.feature.terminal.selection

import com.vpsmanager.terminalengine.CellSnapshot

/**
 * The three character classes that define where a word starts and ends. It is
 * the classic terminal split (xterm, Termux, iTerm): running over
 * `git commit --amend` and double-tapping `commit` selects `commit`, not the
 * whole line and not a single letter.
 */
private enum class CharClass { SPACE, WORD, OTHER }

/**
 * `_` counts as WORD because file names, environment variables and code
 * identifiers use it mid-word — breaking there would make the double tap
 * useless on precisely the text most often copied out of a terminal. `-`, `.`
 * and `/` stay OUT: paths and flags are compound structures, and someone
 * double-tapping `/etc/nginx/nginx.conf` almost always wants one segment, not
 * the whole path (for the whole path there is the triple tap).
 */
private fun classify(codepoint: Int): CharClass = when {
    // A cell never written by the renderer (the same convention as
    // `buildRowDrawOps`) counts as a space, not as content.
    codepoint == 0 -> CharClass.SPACE
    Character.isWhitespace(codepoint) -> CharClass.SPACE
    codepoint == '_'.code -> CharClass.WORD
    Character.isLetterOrDigit(codepoint) -> CharClass.WORD
    else -> CharClass.OTHER
}

/**
 * The class of cell [col] on row [row]. The tail of a wide character
 * (`SPACER_TAIL`) has no codepoint of its own — it belongs to the character to
 * its left, and classifying it in isolation would cut a CJK word in half.
 */
private fun cellClass(snapshot: CellSnapshot, row: Int, col: Int): CharClass {
    val cell = snapshot.cellAt(col, row)
    if (cell.wide == CellSnapshot.Wide.SPACER_TAIL && col > 0) {
        return classify(snapshot.cellAt(col - 1, row).codepoint)
    }
    return classify(cell.codepoint)
}

/**
 * The word under the tapped cell — the double-tap gesture, which is the
 * language every Android text field already speaks.
 *
 * The selection extends both ways for as long as the character class does not
 * change. Double-tapping a space selects the run of spaces, and not nothing:
 * it is xterm's behaviour, and it keeps the double tap from becoming a dead
 * gesture when the finger lands a pixel to the side of the word.
 */
fun selectWord(snapshot: CellSnapshot, row: Int, col: Int): GridSelection {
    require(row in 0 until snapshot.rows) { "linha $row fora da grade de ${snapshot.rows}" }
    require(col in 0 until snapshot.cols) { "coluna $col fora da grade de ${snapshot.cols}" }

    val className = cellClass(snapshot, row, col)
    var start = col
    while (start > 0 && cellClass(snapshot, row, start - 1) == className) start--
    var end = col
    while (end < snapshot.cols - 1 && cellClass(snapshot, row, end + 1) == className) end++

    return GridSelection(startRow = row, startCol = start, endRow = row, endCol = end)
}

/**
 * The LOGICAL line running through [row] — the triple-tap gesture.
 *
 * "Logical", not "on screen": a long line the terminal wrapped to fit the
 * grid's width occupies several screen rows, and selecting only the visible
 * stretch would hand over a path or a URL cut in half. The snapshot's own soft
 * wrap marks ([CellSnapshot.isWrapped] / [CellSnapshot.isWrapContinuation])
 * are what tell the two cases apart — the same ones [extractSelectedText] uses
 * so as not to insert a `\n` where the terminal merely folded the text.
 *
 * The selection ends at the last column with content, not at the grid's width:
 * dragging the highlight across a desert of never-written cells would be
 * visual noise, and the copied text is the same either way.
 */
fun selectLine(snapshot: CellSnapshot, row: Int): GridSelection {
    require(row in 0 until snapshot.rows) { "linha $row fora da grade de ${snapshot.rows}" }

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

/** The whole grid — the system floating bar's "Select all". */
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
