package com.vpsmanager.feature.terminal.selection

import com.vpsmanager.terminalengine.CellSnapshot
import org.junit.Assert.assertEquals
import org.junit.Test

/** Writes [text] from column 0 of row [line]; the rest stays "never written" (codepoint 0). */
private fun grid(
    cols: Int,
    rows: Int,
    rowFlags: ByteArray = ByteArray(rows),
    vararg lines: String,
): CellSnapshot = buildSnapshot(cols = cols, rows = rows, rowFlags = rowFlags) { row, col ->
    val text = lines.getOrNull(row) ?: ""
    narrowCell(if (col < text.length) text[col].code else 0)
}

/**
 * Double-tap (word) and triple-tap (line) selection — the idiom every Android
 * text field speaks and the app's terminal did not.
 *
 * Before this, the only way to select was a long press followed by a drag,
 * cell by cell: to copy a filename the operator had to aim at the first letter
 * and drag to the last, with no highlight on screen to check against (the
 * `TerminalCanvas` never drew a selection).
 */
class WordSelectionTest {

    // ---- Word ---------------------------------------------------------

    @Test
    fun tapInMiddleOfWord_selectsWholeWord() {
        val snapshot = grid(cols = 24, rows = 1, lines = arrayOf("git commit --amend"))

        // The finger lands on the "m" of "commit" (columns 6..11).
        val selection = selectWord(snapshot, row = 0, col = 8)

        assertEquals(GridSelection(0, 4, 0, 9), selection)
        assertEquals("commit", extractSelectedText(snapshot, selection))
    }

    @Test
    fun wordDoesNotCrossNeighbourSpace() {
        val snapshot = grid(cols = 24, rows = 1, lines = arrayOf("git commit --amend"))

        val first = selectWord(snapshot, row = 0, col = 0)

        assertEquals("git", extractSelectedText(snapshot, first))
    }

    @Test
    fun underscoreIsPartOfWord_butHyphenIsNot() {
        // A `_` in the middle of an identifier is content; a `-` separates a
        // flag from its name, and breaking there is what makes the double tap
        // useful on `--amend`.
        val snapshot = grid(cols = 32, rows = 1, lines = arrayOf("VPS_MANAGER_HOME --dry-run"))

        assertEquals("VPS_MANAGER_HOME", extractSelectedText(snapshot, selectWord(snapshot, 0, 5)))
        assertEquals("dry", extractSelectedText(snapshot, selectWord(snapshot, 0, 20)))
    }

    @Test
    fun punctuationGroupsWithPunctuation() {
        // `--` is a single block: two characters of the same class. Without
        // this, double-tapping a `--` would select a lone dash.
        val snapshot = grid(cols = 16, rows = 1, lines = arrayOf("ls --all"))

        assertEquals("--", extractSelectedText(snapshot, selectWord(snapshot, 0, 3)))
    }

    @Test
    fun tapOnSpace_selectsSpaceRun_notNothing() {
        // One pixel beside the word must not turn the gesture into nothing:
        // that would be a double tap that "sometimes doesn't work".
        val snapshot = grid(cols = 16, rows = 1, lines = arrayOf("ab    cd"))

        val selection = selectWord(snapshot, row = 0, col = 3)

        assertEquals(GridSelection(0, 2, 0, 5), selection)
    }

    @Test
    fun neverWrittenCellCountsAsSpace() {
        // The tail of the row is renderer padding (codepoint 0), not text.
        val snapshot = grid(cols = 10, rows = 1, lines = arrayOf("ab"))

        val selection = selectWord(snapshot, row = 0, col = 7)

        assertEquals(GridSelection(0, 2, 0, 9), selection)
        assertEquals("o padding não vira texto ao ser copiado", "", extractSelectedText(snapshot, selection))
    }

    @Test
    fun wideChar_doesNotSplitWord() {
        // A CJK character occupies two cells: the second is SPACER_TAIL and
        // has no codepoint of its own. Classifying it in isolation would split
        // the word.
        val snapshot = buildSnapshot(cols = 6, rows = 1) { _, col ->
            when (col) {
                0 -> wideCell('世'.code)
                1 -> narrowCell(0).copy(wide = CellSnapshot.Wide.SPACER_TAIL)
                2 -> wideCell('界'.code)
                3 -> narrowCell(0).copy(wide = CellSnapshot.Wide.SPACER_TAIL)
                else -> narrowCell(' '.code)
            }
        }

        val selection = selectWord(snapshot, row = 0, col = 0)

        assertEquals(GridSelection(0, 0, 0, 3), selection)
        assertEquals("世界", extractSelectedText(snapshot, selection))
    }

    // ---- Line -----------------------------------------------------------

    @Test
    fun tripleTap_selectsLineUpToLastWrittenChar() {
        val snapshot = grid(cols = 20, rows = 2, lines = arrayOf("primeira", "segunda"))

        val selection = selectLine(snapshot, row = 1)

        assertEquals(GridSelection(1, 0, 1, 6), selection)
        assertEquals("segunda", extractSelectedText(snapshot, selection))
    }

    @Test
    fun tripleTap_takesWholeLogicalLineWhenTerminalWrappedText() {
        // A long line that did not fit the grid's width occupies three SCREEN
        // rows. Selecting only the visible run would hand back a path cut in
        // half — the classic defect of copying from a terminal.
        val flags = byteArrayOf(0x01, 0x03, 0x02, 0x00)
        val snapshot = grid(
            cols = 8,
            rows = 4,
            rowFlags = flags,
            lines = arrayOf("/opt/pan", "el/tools", "/bin", "outra"),
        )

        val selection = selectLine(snapshot, row = 1)

        assertEquals(GridSelection(0, 0, 2, 3), selection)
        assertEquals(
            "a quebra suave não pode virar quebra de linha no texto copiado",
            "/opt/panel/tools/bin",
            extractSelectedText(snapshot, selection),
        )
    }

    @Test
    fun blankLine_givesValidEmptySelection() {
        val snapshot = grid(cols = 8, rows = 2, lines = arrayOf("algo", ""))

        val selection = selectLine(snapshot, row = 1)

        assertEquals(GridSelection(1, 0, 1, 0), selection)
        assertEquals("", extractSelectedText(snapshot, selection))
    }

    // ---- Select all -------------------------------------------------

    @Test
    fun selectAll_coversWholeGrid() {
        val snapshot = grid(cols = 6, rows = 3, lines = arrayOf("um", "dois", "tres"))

        val selection = selectAll(snapshot)

        assertEquals(GridSelection(0, 0, 2, 5), selection)
        assertEquals("um\ndois\ntres", extractSelectedText(snapshot, selection))
    }
}
