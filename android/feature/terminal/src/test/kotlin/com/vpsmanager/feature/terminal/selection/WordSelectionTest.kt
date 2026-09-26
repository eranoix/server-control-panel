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

/** Double-tap (word), triple-tap (line) and select-all, as in Android text fields. */
class WordSelectionTest {

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
        // `_` is part of identifiers; `-` separates a flag from its name.
        val snapshot = grid(cols = 32, rows = 1, lines = arrayOf("VPS_MANAGER_HOME --dry-run"))

        assertEquals("VPS_MANAGER_HOME", extractSelectedText(snapshot, selectWord(snapshot, 0, 5)))
        assertEquals("dry", extractSelectedText(snapshot, selectWord(snapshot, 0, 20)))
    }

    @Test
    fun punctuationGroupsWithPunctuation() {
        // `--` is one run of the same character class, not a lone dash.
        val snapshot = grid(cols = 16, rows = 1, lines = arrayOf("ls --all"))

        assertEquals("--", extractSelectedText(snapshot, selectWord(snapshot, 0, 3)))
    }

    @Test
    fun tapOnSpace_selectsSpaceRun_notNothing() {
        // Tapping just beside a word must still select something.
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
        assertEquals("padding does not become text when copied", "", extractSelectedText(snapshot, selection))
    }

    @Test
    fun wideChar_doesNotSplitWord() {
        // A CJK character's second cell is SPACER_TAIL with no codepoint; classifying
        // it alone would split the word.
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

    @Test
    fun tripleTap_selectsLineUpToLastWrittenChar() {
        val snapshot = grid(cols = 20, rows = 2, lines = arrayOf("first", "another"))

        val selection = selectLine(snapshot, row = 1)

        assertEquals(GridSelection(1, 0, 1, 6), selection)
        assertEquals("another", extractSelectedText(snapshot, selection))
    }

    @Test
    fun tripleTap_takesWholeLogicalLineWhenTerminalWrappedText() {
        // A soft-wrapped line spans three screen rows; selecting only one would cut
        // the path in pieces.
        val flags = byteArrayOf(0x01, 0x03, 0x02, 0x00)
        val snapshot = grid(
            cols = 8,
            rows = 4,
            rowFlags = flags,
            lines = arrayOf("/opt/pan", "el/tools", "/bin", "other"),
        )

        val selection = selectLine(snapshot, row = 1)

        assertEquals(GridSelection(0, 0, 2, 3), selection)
        assertEquals(
            "a soft wrap must not become a line break in the copied text",
            "/opt/panel/tools/bin",
            extractSelectedText(snapshot, selection),
        )
    }

    @Test
    fun blankLine_givesValidEmptySelection() {
        val snapshot = grid(cols = 8, rows = 2, lines = arrayOf("text", ""))

        val selection = selectLine(snapshot, row = 1)

        assertEquals(GridSelection(1, 0, 1, 0), selection)
        assertEquals("", extractSelectedText(snapshot, selection))
    }

    @Test
    fun selectAll_coversWholeGrid() {
        val snapshot = grid(cols = 6, rows = 3, lines = arrayOf("one", "two", "three"))

        val selection = selectAll(snapshot)

        assertEquals(GridSelection(0, 0, 2, 5), selection)
        assertEquals("one\ntwo\nthree", extractSelectedText(snapshot, selection))
    }
}
