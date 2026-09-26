package dev.servercontrolpanel.feature.terminal.input

import android.view.KeyEvent
import android.view.View
import dev.servercontrolpanel.feature.terminal.prefs.TypingMode
import dev.servercontrolpanel.feature.terminal.selection.GridSelection
import dev.servercontrolpanel.feature.terminal.selection.GridSelectionHolder
import dev.servercontrolpanel.terminalengine.KeyByteEncoder
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment

/**
 * Drives the `InputConnection` API directly (no IME) and asserts the exact bytes in
 * [RecordingByteSink]. What real IMEs actually call must still be checked on a device.
 */
@RunWith(RobolectricTestRunner::class)
class TerminalInputConnectionTest {

    private lateinit var sink: RecordingByteSink
    private lateinit var view: View
    private var fakeNowNanos: Long = 0L

    @Before
    fun setUp() {
        sink = RecordingByteSink()
        view = View(RuntimeEnvironment.getApplication())
    }

    private var seenComposition: String = ""

    private fun newConnection(
        cursorMode: KeyByteEncoder.CursorMode = KeyByteEncoder.CursorMode.NORMAL,
        mode: TypingMode = TypingMode.DEFAULT,
    ): TerminalInputConnection = TerminalInputConnection(
        view = view,
        sink = sink,
        cursorMode = cursorMode,
        mode = { mode },
        onCompositionChange = { text -> seenComposition = text },
        nowNanos = { fakeNowNanos },
    )

    private fun keyDown(code: Int, metaState: Int = 0): KeyEvent =
        KeyEvent(0L, 0L, KeyEvent.ACTION_DOWN, code, 0, metaState)

    private fun keyUp(code: Int, metaState: Int = 0): KeyEvent =
        KeyEvent(0L, 0L, KeyEvent.ACTION_UP, code, 0, metaState)

    @Test
    fun `commitText sends exactly the committed bytes once`() {
        val connection = newConnection()
        connection.commitText("ls", 1)
        assertEquals("6c 73", sink.hex())
    }

    @Test
    fun `in TEXT mode the composition is held until committed`() {
        val connection = newConnection(mode = TypingMode.TEXT)
        connection.setComposingText("ni", 1)
        connection.setComposingText("nih", 1)
        assertTrue("composing must not touch the sink", sink.isEmpty())

        connection.commitText("你好", 1)
        assertEquals("e4 bd a0 e5 a5 bd", sink.hex())
    }

    @Test
    fun `in TERMINAL mode the composition is not held and each new piece goes out immediately`() {
        // Some keyboards (Samsung) compose despite `TYPE_NULL`; TERMINAL mode must still
        // echo every keystroke immediately.
        val connection = newConnection(mode = TypingMode.TERMINAL)
        connection.setComposingText("l", 1)
        assertEquals("6c", sink.hex())
        connection.setComposingText("ls", 1)
        assertEquals("6c 73", sink.hex())

        // Committing what was already sent must not send it again.
        connection.commitText("ls", 1)
        assertEquals("committing must not duplicate what was already echoed", "6c 73", sink.hex())
    }

    @Test
    fun `in TERMINAL mode shrinking the composition sends DEL`() {
        val connection = newConnection(mode = TypingMode.TERMINAL)
        connection.setComposingText("ls", 1)
        assertEquals("6c 73", sink.hex())
        connection.setComposingText("l", 1)
        assertEquals("6c 73 7f", sink.hex())
    }

    @Test
    fun `in TERMINAL mode a word replacement erases what was already echoed`() {
        // Autocorrect replaces the echoed "teh" with "the"; without erasing, the
        // screen would read "tehthe".
        val connection = newConnection(mode = TypingMode.TERMINAL)
        connection.setComposingText("teh", 1)
        assertEquals("74 65 68", sink.hex())

        connection.commitText("the", 1)
        assertEquals("74 65 68 7f 7f 7f 74 68 65", sink.hex())
    }

    @Test
    fun `finishComposingText delivers the word in flight instead of losing it`() {
        // `finishComposingText` means "keep it as it stands", so flush the composition
        // (Termux does the same).
        val connection = newConnection(mode = TypingMode.TEXT)
        connection.setComposingText("hel", 1)
        assertTrue("nothing goes out while composing", sink.isEmpty())

        connection.finishComposingText()
        assertEquals("68 65 6c", sink.hex())
        assertEquals("", connection.composingTextForTest())
    }

    @Test
    fun `a command key delivers the composition before itself`() {
        // A word in flight must be sent before the Enter (0d) that submits it.
        val connection = newConnection(mode = TypingMode.TEXT)
        connection.setComposingText("ls", 1)
        connection.sendKeyEvent(keyDown(KeyEvent.KEYCODE_ENTER))
        assertEquals("6c 73 0d", sink.hex())
    }

    @Test
    fun `the composition strip is notified on every change and at the end`() {
        val connection = newConnection(mode = TypingMode.TEXT)
        connection.setComposingText("te", 1)
        assertEquals("te", seenComposition)
        connection.setComposingText("tes", 1)
        assertEquals("tes", seenComposition)

        connection.commitText("test", 1)
        assertEquals("once the word is committed the strip disappears", "", seenComposition)
    }

    @Test
    fun `an IME-issued key event matching a just-committed word is dropped, not duplicated`() {
        val connection = newConnection()
        connection.commitText("word", 1)
        val afterCommit = sink.hex()

        // Some IMEs synthesize a matching sendKeyEvent for characters they
        // just committed. 'w' is next in the dedup queue and arrives well
        // inside the dedup window.
        fakeNowNanos += 10_000_000L // +10ms, still inside the 150ms window
        connection.sendKeyEvent(keyDown(KeyEvent.KEYCODE_W))
        connection.sendKeyEvent(keyUp(KeyEvent.KEYCODE_W))

        assertEquals("bytes must appear once, not twice", afterCommit, sink.hex())
    }

    @Test
    fun `deleteSurroundingText with no composing region sends one DEL byte`() {
        val connection = newConnection()
        connection.deleteSurroundingText(1, 0)
        assertEquals("7f", sink.hex())
    }

    @Test
    fun `deleteSurroundingText during composition shrinks the buffer locally with zero bytes`() {
        val connection = newConnection(mode = TypingMode.TEXT)
        connection.setComposingText("ab", 1)
        connection.deleteSurroundingText(1, 0)
        assertTrue("no bytes must reach the sink mid-composition", sink.isEmpty())
        assertEquals("a", connection.composingTextForTest())

        connection.commitText(connection.composingTextForTest(), 1)
        assertEquals("61", sink.hex())
    }

    @Test
    fun `hardware key events encode arrows, ctrl, alt, tab, esc and enter`() {
        val connection = newConnection()

        connection.sendKeyEvent(keyDown(KeyEvent.KEYCODE_DPAD_UP))
        assertEquals("1b 5b 41", sink.hex())

        connection.sendKeyEvent(keyDown(KeyEvent.KEYCODE_C, KeyEvent.META_CTRL_ON))
        assertEquals("1b 5b 41 03", sink.hex())

        connection.sendKeyEvent(keyDown(KeyEvent.KEYCODE_F, KeyEvent.META_ALT_ON))
        assertEquals("1b 5b 41 03 1b 66", sink.hex())

        connection.sendKeyEvent(keyDown(KeyEvent.KEYCODE_TAB))
        assertEquals("1b 5b 41 03 1b 66 09", sink.hex())

        connection.sendKeyEvent(keyDown(KeyEvent.KEYCODE_ESCAPE))
        assertEquals("1b 5b 41 03 1b 66 09 1b", sink.hex())

        connection.sendKeyEvent(keyDown(KeyEvent.KEYCODE_ENTER))
        assertEquals("1b 5b 41 03 1b 66 09 1b 0d", sink.hex())
    }

    @Test
    fun `key-up events never emit bytes on their own`() {
        val connection = newConnection()
        connection.sendKeyEvent(keyUp(KeyEvent.KEYCODE_ENTER))
        assertTrue(sink.isEmpty())
    }

    @Test
    fun `terminal content is never exposed to the IME in any mode`() {
        // Privacy: the keyboard is a third-party app and must never see grid content
        // (output, tokens, passwords). Only the fixed virtual-context sentinels are exposed.
        for (mode in TypingMode.entries) {
            sink.clear()
            val connection = newConnection(mode = mode)
            connection.commitText("hello", 1) // went to the terminal, not to the IME

            val exposed = connection.getTextBeforeCursor(10, 0).toString() +
                connection.getTextAfterCursor(10, 0).toString() +
                (connection.getExtractedText(null, 0)?.text?.toString() ?: "")

            assertTrue(
                "terminal content leaked to the IME in $mode: $exposed",
                !exposed.contains("hello"),
            )
            // With no composition in flight, the keyboard sees no letters at all.
            assertEquals(
                "only the sentinels may be there in $mode",
                "",
                exposed.filter { it.isLetterOrDigit() },
            )
            assertNull("selection belongs to the grid, not the IME", connection.getSelectedText(0))
        }
    }

    @Test
    fun `in TEXT mode autocorrect sees the composition`() {
        // Autocorrect needs to see the composition, or it corrects against nothing.
        val connection = newConnection(mode = TypingMode.TEXT)
        connection.setComposingText("comec", 1)

        // The composition ends the text before the cursor; the sentinel before it is not
        // a letter, so it does not join the word.
        assertTrue(connection.getTextBeforeCursor(10, 0).toString().endsWith("comec"))
        assertEquals("ec", connection.getTextBeforeCursor(2, 0).toString())

        val extracted = connection.getExtractedText(null, 0)!!
        assertTrue(
            "the composition must appear in the extracted text",
            extracted.text.toString().contains("comec"),
        )
        // The cursor sits after the composition and before the right sentinel.
        assertEquals("comec", extracted.text.toString().substring(1, extracted.selectionStart))
    }

    @Test
    fun `in TERMINAL mode the keyboard gets no text but does get a cursor`() {
        // Samsung keyboards ignore `TYPE_NULL` and swallow arrow keys when the cursor
        // looks like it is at a boundary, so expose sentinels but no text.
        val connection = newConnection(mode = TypingMode.TERMINAL)
        connection.setComposingText("comec", 1)

        val before = connection.getTextBeforeCursor(10, 0).toString()
        assertEquals("no letter may appear here", "", before.filter { it.isLetterOrDigit() })
        assertTrue("but it must not be empty, which made the keyboard swallow the arrow", before.isNotEmpty())
        assertTrue(connection.getTextAfterCursor(10, 0).toString().isNotEmpty())
    }

    @Test
    fun `grid selection and ime composition state do not observe each other`() {
        val connection = newConnection(mode = TypingMode.TEXT)
        val selectionHolder = GridSelectionHolder()

        // Direction 1: composition in progress, then a selection is set.
        connection.setComposingText("ni", 1)
        selectionHolder.selection = GridSelection(startRow = 2, startCol = 3, endRow = 2, endCol = 7)
        assertEquals("selection must not touch composing state", "ni", connection.composingTextForTest())

        connection.setComposingText("nih", 1)
        connection.commitText("你好", 1)
        assertEquals(
            "composition committed unaffected by the selection change",
            "e4 bd a0 e5 a5 bd",
            sink.hex(),
        )

        // Direction 2: a selection exists, then composition starts and commits.
        val expectedSelection = GridSelection(startRow = 0, startCol = 0, endRow = 0, endCol = 4)
        selectionHolder.selection = expectedSelection
        connection.setComposingText("h", 1)
        // Flushes "h" to the terminal; the selection must not move with it.
        connection.finishComposingText()
        assertEquals(
            "composition must not touch the selection",
            expectedSelection,
            selectionHolder.selection,
        )
    }

    // Virtual context: Samsung's Honeyboard swallows arrow keys when both sides of the
    // cursor are empty. Sentinels prevent that and must never become bytes.
    // See https://github.com/termux/termux-app/pull/5287

    @Test
    fun `neither side of the cursor is empty`() {
        for (mode in TypingMode.entries) {
            val connection = newConnection(mode = mode)
            assertTrue(
                "before the cursor must not be empty in $mode",
                connection.getTextBeforeCursor(20, 0)!!.isNotEmpty(),
            )
            assertTrue(
                "after the cursor must not be empty in $mode",
                connection.getTextAfterCursor(20, 0)!!.isNotEmpty(),
            )
        }
    }

    @Test
    fun `a sentinel never becomes a byte in the terminal`() {
        // The virtual context is IME metadata only; a Private Use character in the output
        // would reach the grid and the PTY.
        for (mode in TypingMode.entries) {
            sink.clear()
            val connection = newConnection(mode = mode)
            connection.getTextBeforeCursor(20, 0)
            connection.getTextAfterCursor(20, 0)
            connection.getExtractedText(null, 0)
            connection.setComposingText("ola", 1)
            connection.finishComposingText()
            connection.commitText("ls", 1)
            connection.deleteSurroundingText(1, 0)

            val output = String(sink.bytes(), Charsets.UTF_8)
            assertTrue(
                "left sentinel leaked to the terminal in $mode: ${sink.hex()}",
                !output.contains('\uE000'),
            )
            assertTrue(
                "right sentinel leaked to the terminal in $mode: ${sink.hex()}",
                !output.contains('\uE001'),
            )
        }
    }

    @Test
    fun `the real word stays readable to autocorrect without the sentinel attached`() {
        val connection = newConnection(mode = TypingMode.TEXT)
        connection.setComposingText("ola", 1)
        val before = connection.getTextBeforeCursor(20, 0).toString()

        // The Private Use sentinel is not a letter, so word segmentation stops at it.
        assertTrue("the real composition must be there: $before", before.endsWith("ola"))
        val lastWord = before.takeLastWhile { it.isLetter() }
        assertEquals("ola", lastWord)
    }

    @Test
    fun `the extracted text agrees with the getters, cursor in the middle`() {
        // All three answers must agree, or the IME may again think the cursor is at a boundary.
        val connection = newConnection(mode = TypingMode.TEXT)
        connection.setComposingText("ola", 1)

        val extracted = connection.getExtractedText(null, 0)!!
        val text = extracted.text.toString()
        assertTrue("there must be text after the cursor", extracted.selectionStart < text.length)
        assertTrue("there must be text before the cursor", extracted.selectionStart > 0)
        assertEquals(extracted.selectionStart, extracted.selectionEnd)
        assertEquals("ola", text.substring(1, extracted.selectionStart))
    }

    @Test
    fun `truncation by n honors the keyboard's request`() {
        val connection = newConnection(mode = TypingMode.TEXT)
        connection.setComposingText("command", 1)
        assertEquals(3, connection.getTextBeforeCursor(3, 0)!!.length)
        assertEquals("", connection.getTextBeforeCursor(0, 0)!!.toString())
        assertEquals("", connection.getTextAfterCursor(0, 0)!!.toString())
    }

    @Test
    fun `autocorrect word replacement does not eat the sentinel`() {
        // Counting the sentinel would delete one character too many, sending a stray DEL.
        val connection = newConnection(mode = TypingMode.TEXT)
        connection.setComposingText("ola", 1)
        sink.clear()
        connection.deleteSurroundingText(3, 0) // exactly the word
        assertTrue("nothing should reach the terminal, the word only existed in the IME", sink.isEmpty())
        assertEquals("", connection.composingTextForTest())
    }

    @Test
    fun `a delete larger than the composition does not invent extra DELs`() {
        val connection = newConnection(mode = TypingMode.TEXT)
        connection.setComposingText("ola", 1)
        sink.clear()
        connection.deleteSurroundingText(99, 0)
        assertTrue("the composition is local, nothing goes to the terminal", sink.isEmpty())
    }

    @Test
    fun `batch edits are accepted and respect nesting`() {
        // Returning `false` may make the IME give up on word replacement.
        val connection = newConnection(mode = TypingMode.TEXT)
        assertTrue(connection.beginBatchEdit())
        assertTrue("inner batch still open", connection.beginBatchEdit())
        assertTrue("one batch still remains", connection.endBatchEdit())
        assertEquals(false, connection.endBatchEdit())
        assertEquals(false, connection.endBatchEdit()) // never goes negative
    }

    @Test
    fun `auto-capitalization answers in TEXT mode and stays off in TERMINAL`() {
        // TerminalInputView declares CAP_SENTENCES in TEXT mode, so this must honor it.
        val text = newConnection(mode = TypingMode.TEXT)
        assertTrue(
            "the start of a sentence must request a capital",
            text.getCursorCapsMode(android.text.InputType.TYPE_TEXT_FLAG_CAP_SENTENCES) != 0,
        )
        val terminal = newConnection(mode = TypingMode.TERMINAL)
        assertEquals(
            0,
            terminal.getCursorCapsMode(android.text.InputType.TYPE_TEXT_FLAG_CAP_SENTENCES),
        )
    }

}
