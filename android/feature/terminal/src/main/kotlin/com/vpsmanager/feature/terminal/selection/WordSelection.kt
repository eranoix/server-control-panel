package com.vpsmanager.feature.terminal.selection

import com.vpsmanager.terminalengine.CellSnapshot

/**
 * The three character classes that define where a word starts and ends. It is
 * the classic terminal split (xterm, Termux, iTerm): running over
 * `git commit --amend` and double-tapping `commit` selects `commit`, not the
 * whole line and not a single letter.
 */
private enum class ClasseDeCaractere { ESPACO, PALAVRA, OUTRO }

/**
 * `_` counts as WORD because file names, environment variables and code
 * identifiers use it mid-word — breaking there would make the double tap
 * useless on precisely the text most often copied out of a terminal. `-`, `.`
 * and `/` stay OUT: paths and flags are compound structures, and someone
 * double-tapping `/etc/nginx/nginx.conf` almost always wants one segment, not
 * the whole path (for the whole path there is the triple tap).
 */
private fun classificar(codepoint: Int): ClasseDeCaractere = when {
    // A cell never written by the renderer (the same convention as
    // `buildRowDrawOps`) counts as a space, not as content.
    codepoint == 0 -> ClasseDeCaractere.ESPACO
    Character.isWhitespace(codepoint) -> ClasseDeCaractere.ESPACO
    codepoint == '_'.code -> ClasseDeCaractere.PALAVRA
    Character.isLetterOrDigit(codepoint) -> ClasseDeCaractere.PALAVRA
    else -> ClasseDeCaractere.OUTRO
}

/**
 * The class of cell [col] on row [row]. The tail of a wide character
 * (`SPACER_TAIL`) has no codepoint of its own — it belongs to the character to
 * its left, and classifying it in isolation would cut a CJK word in half.
 */
private fun classeDaCelula(snapshot: CellSnapshot, row: Int, col: Int): ClasseDeCaractere {
    val cell = snapshot.cellAt(col, row)
    if (cell.wide == CellSnapshot.Wide.SPACER_TAIL && col > 0) {
        return classificar(snapshot.cellAt(col - 1, row).codepoint)
    }
    return classificar(cell.codepoint)
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
fun selecionarPalavra(snapshot: CellSnapshot, row: Int, col: Int): GridSelection {
    require(row in 0 until snapshot.rows) { "linha $row fora da grade de ${snapshot.rows}" }
    require(col in 0 until snapshot.cols) { "coluna $col fora da grade de ${snapshot.cols}" }

    val classe = classeDaCelula(snapshot, row, col)
    var inicio = col
    while (inicio > 0 && classeDaCelula(snapshot, row, inicio - 1) == classe) inicio--
    var fim = col
    while (fim < snapshot.cols - 1 && classeDaCelula(snapshot, row, fim + 1) == classe) fim++

    return GridSelection(startRow = row, startCol = inicio, endRow = row, endCol = fim)
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
fun selecionarLinha(snapshot: CellSnapshot, row: Int): GridSelection {
    require(row in 0 until snapshot.rows) { "linha $row fora da grade de ${snapshot.rows}" }

    var primeira = row
    while (primeira > 0 && snapshot.isWrapContinuation(primeira)) primeira--
    var ultima = row
    while (ultima < snapshot.rows - 1 && snapshot.isWrapped(ultima)) ultima++

    return GridSelection(
        startRow = primeira,
        startCol = 0,
        endRow = ultima,
        endCol = ultimaColunaComConteudo(snapshot, ultima),
    )
}

/** The whole grid — the system floating bar's "Select all". */
fun selecionarTudo(snapshot: CellSnapshot): GridSelection = GridSelection(
    startRow = 0,
    startCol = 0,
    endRow = snapshot.rows - 1,
    endCol = snapshot.cols - 1,
)

/** Last written column of the row, or 0 on a blank row (an empty but valid selection). */
private fun ultimaColunaComConteudo(snapshot: CellSnapshot, row: Int): Int {
    for (col in snapshot.cols - 1 downTo 0) {
        if (snapshot.cellAt(col, row).codepoint != 0) return col
    }
    return 0
}
