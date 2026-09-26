package com.vpsmanager.feature.terminal.selection

import com.vpsmanager.terminalengine.CellSnapshot
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/** Never-written cell: narrow with codepoint 0, the padding convention `RowDrawOps` also uses. */
private fun blankCell(): CellSnapshot.Cell = narrowCell(0)

private const val WRAPPED_FLAG = 0x01

class SelectionClipboardTest {

    @Test
    fun extractSelectedText_wrappedRow_joinsWithoutInsertingALineBreak() {
        // Row 0 is "abcde" and soft-wraps into row 1 "fghij" -- one logical
        // line split by the fixed 5-col width, not two real lines.
        val snapshot = buildSnapshot(
            cols = 5,
            rows = 2,
            rowFlags = byteArrayOf(WRAPPED_FLAG.toByte(), 0),
        ) { row, col ->
            val text = if (row == 0) "abcde" else "fghij"
            narrowCell(text[col].code)
        }

        val text = extractSelectedText(snapshot, GridSelection(startRow = 0, startCol = 0, endRow = 1, endCol = 4))

        assertEquals("abcdefghij", text)
    }

    @Test
    fun extractSelectedText_genuineNewline_isPreservedBetweenNonWrappedRows() {
        val snapshot = buildSnapshot(cols = 3, rows = 2) { row, col ->
            val text = if (row == 0) "abc" else "def"
            narrowCell(text[col].code)
        }

        val text = extractSelectedText(snapshot, GridSelection(startRow = 0, startCol = 0, endRow = 1, endCol = 2))

        assertEquals("abc\ndef", text)
    }

    @Test
    fun extractSelectedText_shortRow_trimsOnlyTrailingNeverWrittenPadding() {
        // "hi" printed, then the rest of the row was never written (codepoint 0).
        val snapshot = buildSnapshot(cols = 5, rows = 1) { _, col ->
            when (col) {
                0 -> narrowCell('h'.code)
                1 -> narrowCell('i'.code)
                else -> blankCell()
            }
        }

        val text = extractSelectedText(snapshot, GridSelection(startRow = 0, startCol = 0, endRow = 0, endCol = 4))

        assertEquals("hi", text)
    }

    @Test
    fun extractSelectedText_genuineTrailingSpace_isKeptNotTrimmed() {
        // "hi " -- the shell actually printed a real 0x20 space as the last
        // character, followed by never-written padding. Only the padding trims.
        val snapshot = buildSnapshot(cols = 5, rows = 1) { _, col ->
            when (col) {
                0 -> narrowCell('h'.code)
                1 -> narrowCell('i'.code)
                2 -> narrowCell(' '.code)
                else -> blankCell()
            }
        }

        val text = extractSelectedText(snapshot, GridSelection(startRow = 0, startCol = 0, endRow = 0, endCol = 4))

        assertEquals("hi ", text)
    }

    @Test
    fun extractSelectedText_doubleWidthGlyph_collapsesToOneCharacterNoExtraPadding() {
        // A CJK-style wide glyph at col 0 occupies col 0 (WIDE) + col 1 (SPACER_TAIL),
        // then "x" at col 2.
        val snapshot = buildSnapshot(cols = 3, rows = 1) { _, col ->
            when (col) {
                0 -> wideCell(0x4e2d) // 中
                1 -> spacerTailCell()
                else -> narrowCell('x'.code)
            }
        }

        val text = extractSelectedText(snapshot, GridSelection(startRow = 0, startCol = 0, endRow = 0, endCol = 2))

        assertEquals("中x", text)
        assertEquals("exactly 2 characters, never 3", 2, text.length)
    }

    @Test
    fun extractSelectedText_reversedDragDirection_stillReadsInForwardReadingOrder() {
        val snapshot = buildSnapshot(cols = 3, rows = 2) { row, col ->
            val text = if (row == 0) "abc" else "def"
            narrowCell(text[col].code)
        }

        // Dragged from bottom-right to top-left -- start/end are swapped versus reading order.
        val text = extractSelectedText(snapshot, GridSelection(startRow = 1, startCol = 2, endRow = 0, endCol = 0))

        assertEquals("abc\ndef", text)
    }

    @Test
    fun copyAction_noOpWhenNoSelection() {
        val snapshot = buildSnapshot(cols = 1, rows = 1) { _, _ -> narrowCell('a'.code) }
        val written = mutableListOf<String>()
        val action = CopyAction(snapshotProvider = { snapshot }, selectionProvider = { null }, clipboardWrite = { written += it })

        action.copy()

        assertTrue(written.isEmpty())
    }

    @Test
    fun copyAction_noOpWhenNoSnapshotYet() {
        val written = mutableListOf<String>()
        val action = CopyAction(
            snapshotProvider = { null },
            selectionProvider = { GridSelection(0, 0, 0, 0) },
            clipboardWrite = { written += it },
        )

        action.copy()

        assertTrue(written.isEmpty())
    }

    @Test
    fun copyAction_writesExactExtractedTextExactlyOnce() {
        val snapshot = buildSnapshot(cols = 3, rows = 1) { _, col -> narrowCell("abc"[col].code) }
        val written = mutableListOf<String>()
        val action = CopyAction(
            snapshotProvider = { snapshot },
            selectionProvider = { GridSelection(0, 0, 0, 2) },
            clipboardWrite = { written += it },
        )

        action.copy()

        assertEquals(listOf("abc"), written)
    }

    @Test
    fun pasteAction_emptyClipboard_neverCallsSendPaste() {
        var callCount = 0
        val action = PasteAction(clipboardRead = { "" }, sendPaste = { callCount++ })

        action.paste()

        assertEquals(0, callCount)
    }

    @Test
    fun pasteAction_nullClipboard_neverCallsSendPaste() {
        var callCount = 0
        val action = PasteAction(clipboardRead = { null }, sendPaste = { callCount++ })

        action.paste()

        assertEquals(0, callCount)
    }

    @Test
    fun pasteAction_multiParagraphClipboard_sendsExactlyOnceWithTheEntireContent() {
        val multiParagraph = "first line\nsecond line\n\nfourth paragraph with a trailing space "
        var callCount = 0
        val sent = mutableListOf<String>()
        val action = PasteAction(
            clipboardRead = { multiParagraph },
            sendPaste = { callCount++; sent += it },
        )

        action.paste()

        assertEquals("exactly one sendPaste call, never chunked", 1, callCount)
        assertEquals(listOf(multiParagraph), sent)
    }

    // Paste is always on the bar, so with an empty clipboard it must tell the user.

    @Test
    fun pasteAction_emptyClipboard_warnsInsteadOfStayingSilent() {
        var notices = 0
        val action = PasteAction(
            clipboardRead = { "" },
            sendPaste = { fail("must not send any bytes with an empty clipboard") },
            onContentMissing = { notices++ },
        )

        action.paste()

        assertEquals("exactly one notice", 1, notices)
    }

    @Test
    fun pasteAction_missingClipboard_warnsToo() {
        var notices = 0
        val action = PasteAction(
            clipboardRead = { null },
            sendPaste = { fail("must not send any bytes without a clipboard") },
            onContentMissing = { notices++ },
        )

        action.paste()

        assertEquals(1, notices)
    }

    @Test
    fun pasteAction_withContent_doesNotWarn() {
        var notices = 0
        val sent = mutableListOf<String>()
        val action = PasteAction(
            clipboardRead = { "ls -la" },
            sendPaste = { sent += it },
            onContentMissing = { notices++ },
        )

        action.paste()

        assertEquals("the notice is only for the empty case", 0, notices)
        assertEquals(listOf("ls -la"), sent)
    }

    @Test
    fun use_deliversExactlyTextUnderSelection() {
        val snapshot = buildSnapshot(cols = 6, rows = 1) { _, col -> narrowCell("ls -la"[col].code) }
        val received = mutableListOf<String>()

        SelectedText({ snapshot }, { GridSelection(0, 0, 0, 5) }).use { received += it }

        assertEquals(listOf("ls -la"), received)
    }

    @Test
    fun use_withoutSelection_consumesNothing() {
        val snapshot = buildSnapshot(cols = 1, rows = 1) { _, _ -> narrowCell('a'.code) }

        SelectedText({ snapshot }, { null }).use {
            fail("without a selection there is no text to share or resend")
        }
    }

    @Test
    fun use_selectionOfNeverWrittenCells_doesNotOpenEmptyChooser() {
        // A blank selection extracts "", and acting on it would look broken.
        val snapshot = buildSnapshot(cols = 4, rows = 1) { _, _ -> narrowCell(0) }

        SelectedText({ snapshot }, { GridSelection(0, 0, 0, 3) }).use {
            fail("empty text must not reach the consumer")
        }
    }

    @Test
    fun selectedText_readsSnapshotAtClickTime_notStaleCopy() {
        // The program may keep printing while the bar is up; use what is on screen now.
        var current = buildSnapshot(cols = 3, rows = 1) { _, col -> narrowCell("abc"[col].code) }
        val text = SelectedText({ current }, { GridSelection(0, 0, 0, 2) })

        assertEquals("abc", text.read())
        current = buildSnapshot(cols = 3, rows = 1) { _, col -> narrowCell("xyz"[col].code) }
        assertEquals("xyz", text.read())
    }
}
