package dev.servercontrolpanel.feature.terminal.input

import android.text.Editable
import android.text.TextUtils
import android.view.KeyEvent
import android.view.View
import android.view.inputmethod.BaseInputConnection
import android.view.inputmethod.ExtractedText
import android.view.inputmethod.ExtractedTextRequest
import dev.servercontrolpanel.feature.terminal.prefs.TypingMode
import dev.servercontrolpanel.terminalengine.KeyByteEncoder

/**
 * The IME's single door into the terminal. Behaviour follows [TypingMode], read
 * on every call because the user may switch modes mid-session:
 *
 * - [TypingMode.TERMINAL]: [TerminalInputView] declares `TYPE_NULL` and the
 *   keyboard sends key events. If a keyboard composes anyway (e.g. Samsung), each
 *   new piece of the composition goes straight to the terminal and shrinking it
 *   sends DEL, so nothing is held back.
 * - [TypingMode.TEXT]: the composition is kept and returned by the getters, as
 *   the declared `inputType` promises, so autocorrect sees real context; the word
 *   goes to the terminal once confirmed.
 *
 * No `Editable` is materialized (`fullEditor = false`): it would be a second owner
 * of the content competing with the terminal grid. Only the in-flight
 * composition is kept here.
 */
class TerminalInputConnection(
    view: View,
    private val sink: ByteSink,
    private val cursorMode: KeyByteEncoder.CursorMode = KeyByteEncoder.CursorMode.NORMAL,
    private val mode: () -> TypingMode = { TypingMode.DEFAULT },
    private val onCompositionChange: (String) -> Unit = {},
    private val nowNanos: () -> Long = System::nanoTime,
) : BaseInputConnection(view, false) {

    /** What the keyboard believes it is composing right now. */
    private val composing = StringBuilder()

    /**
     * How many leading characters of [composing] were already sent to the
     * terminal. Non-zero only in TERMINAL mode, where the composition is passed
     * through; lets a confirmed word skip what already went out.
     */
    private var alreadySent = 0

    // IME vs. synthetic key dedup: some keyboards call commitText for a word and
    // then synthesise sendKeyEvent for the same characters. For a short window we
    // swallow key events matching what was just sent. 150 ms is far above a
    // same-frame resend (under 16 ms) and below deliberate human typing. A safety
    // net for non-standard keyboards.
    private val dedupQueue = ArrayDeque<Char>()
    private var dedupDeadlineNanos = Long.MIN_VALUE

    override fun getEditable(): Editable? = null

    override fun setComposingText(text: CharSequence, newCursorPosition: Int): Boolean {
        val next = text.toString()
        if (mode().composesText) {
            replaceComposition(next)
            return true
        }

        // TERMINAL mode: nothing may be held by the keyboard. Send the difference
        // now, for the immediate echo a shell expects.
        val commonPrefix = commonPrefix(composing, next)
        if (alreadySent > commonPrefix) {
            send(ByteArray(alreadySent - commonPrefix) { DEL_BYTE })
            alreadySent = commonPrefix
        }
        if (next.length > alreadySent) {
            sendText(next.substring(alreadySent))
            alreadySent = next.length
        }
        replaceComposition(next)
        return true
    }

    override fun setComposingRegion(start: Int, end: Int): Boolean {
        // No Editable to carve a region from; the composition is always set whole.
        return true
    }

    override fun finishComposingText(): Boolean {
        // "The composition stands": send whatever has not gone out yet, or the
        // word would be lost when switching apps or tapping outside mid-word.
        flushPending()
        clearComposition()
        return true
    }

    override fun commitText(text: CharSequence, newCursorPosition: Int): Boolean {
        val committed = text.toString()

        if (alreadySent > 0) {
            // TERMINAL mode with a composing keyboard: part is already on screen.
            // If the commit extends it, send only the remainder; if the keyboard
            // replaced the word, erase what went out before sending the new one.
            if (committed.startsWith(composing.substring(0, alreadySent))) {
                if (committed.length > alreadySent) sendText(committed.substring(alreadySent))
            } else {
                send(ByteArray(alreadySent) { DEL_BYTE })
                sendText(committed)
            }
        } else if (committed.isNotEmpty()) {
            sendText(committed)
        }

        clearComposition()
        return true
    }

    override fun deleteSurroundingText(beforeLength: Int, afterLength: Int): Boolean {
        if (composing.isNotEmpty() && alreadySent == 0) {
            // TEXT mode, deleting inside the composition: nothing reached the
            // terminal, so only shrink the local buffer.
            val remove = minOf(beforeLength, composing.length)
            replaceComposition(composing.substring(0, composing.length - remove))
            return true
        }
        // No pending composition (or deleting what was echoed in TERMINAL mode):
        // the delete goes to the terminal. Samsung's "fix spelling" may ask for
        // beforeLength > 1 at once.
        val remove = if (composing.isEmpty()) beforeLength else minOf(beforeLength, alreadySent)
        if (remove > 0) send(ByteArray(remove) { DEL_BYTE })
        if (composing.isNotEmpty()) {
            alreadySent -= remove
            replaceComposition(composing.substring(0, composing.length - remove))
        }
        return true
    }

    override fun deleteSurroundingTextInCodePoints(beforeLength: Int, afterLength: Int): Boolean =
        deleteSurroundingText(beforeLength, afterLength)

    override fun sendKeyEvent(event: KeyEvent): Boolean {
        if (event.action != KeyEvent.ACTION_DOWN) return true

        val unicodeChar = event.unicodeChar
        if (unicodeChar != 0 && consumeIfDuplicate(unicodeChar.toChar())) {
            return true
        }

        val bytes = KeyByteEncoder.encode(event, cursorMode) ?: return false
        // A command key (Enter, arrows, Ctrl) ends the composition, which must
        // reach the terminal first or the word would arrive after the Enter.
        if (composing.isNotEmpty()) {
            flushPending()
            clearComposition()
        }
        sink.send(bytes)
        return true
    }

    override fun performEditorAction(actionCode: Int): Boolean {
        // Go/Done/Next mean nothing in a terminal; Enter arrives via sendKeyEvent.
        return true
    }

    // What the IME sees. The getters serve two purposes:
    //
    // 1) Autocorrect decides replacements from the text around the cursor, so in
    //    TEXT mode the real composition must show up here.
    // 2) The Samsung keyboard (`com.samsung.android.honeyboard`) checks cursor
    //    boundaries before emitting an event; with empty text on both sides it
    //    assumes the cursor is at a boundary and swallows arrow keys (documented
    //    in https://github.com/termux/termux-app/pull/5287).
    //
    // So a virtual context is exposed: Private Use Area sentinels flank the
    // cursor so neither side is empty. They are IME metadata only and never
    // reach the grid or the PTY; not being letters, they do not join the word
    // during segmentation. This applies in TERMINAL mode too, since Samsung
    // keyboards ignore `TYPE_NULL`.

    override fun getTextBeforeCursor(n: Int, flags: Int): CharSequence {
        if (n <= 0) return ""
        val real = if (mode().composesText) composing.toString() else ""
        val virtual = LEFT_SENTINEL + real
        return if (virtual.length <= n) virtual else virtual.substring(virtual.length - n)
    }

    /**
     * There is no real text after the cursor, but return the sentinel anyway: an
     * empty answer makes Honeyboard suppress the right arrow key.
     */
    override fun getTextAfterCursor(n: Int, flags: Int): CharSequence {
        if (n <= 0) return ""
        return if (RIGHT_SENTINEL.length <= n) RIGHT_SENTINEL else RIGHT_SENTINEL.substring(0, n)
    }

    /** Selection belongs to the grid, never the IME; see `GridSelection`. */
    override fun getSelectedText(flags: Int): CharSequence? = null

    /**
     * The same virtual context, whole, with the cursor in the middle. It must
     * agree with [getTextBeforeCursor] and [getTextAfterCursor], or the IME would
     * again decide the cursor is at a boundary. In TERMINAL mode it holds just the
     * sentinels.
     */
    override fun getExtractedText(request: ExtractedTextRequest?, flags: Int): ExtractedText? {
        val real = if (mode().composesText) composing.toString() else ""
        val text = LEFT_SENTINEL + real + RIGHT_SENTINEL
        val cursor = LEFT_SENTINEL.length + real.length
        return ExtractedText().apply {
            this.text = text
            startOffset = 0
            partialStartOffset = -1
            partialEndOffset = -1
            selectionStart = cursor
            selectionEnd = cursor
        }
    }

    /**
     * Automatic capitalisation for TEXT mode, which declares `CAP_SENTENCES` (the
     * [BaseInputConnection] default answers 0). Computed on the real text only;
     * the sentinels are not punctuation.
     */
    override fun getCursorCapsMode(reqModes: Int): Int {
        if (!mode().composesText) return 0
        return TextUtils.getCapsMode(composing.toString(), composing.length, reqModes)
    }

    // Batch editing: keyboards wrap autocorrect replacements in
    // begin/endBatchEdit, and the default `false` ("no batches") can make an IME
    // skip those operations. Nothing needs deferring here, so batches are only
    // counted and `endBatchEdit` reports whether one is still open, as documented.

    private var openBatches = 0

    override fun beginBatchEdit(): Boolean {
        openBatches++
        return true
    }

    override fun endBatchEdit(): Boolean {
        if (openBatches > 0) openBatches--
        return openBatches > 0
    }

    /** Test only; never called by production code. */
    internal fun composingTextForTest(): String = composing.toString()

    // --- internals ---

    private fun replaceComposition(next: String) {
        if (composing.toString() == next) return
        composing.setLength(0)
        composing.append(next)
        onCompositionChange(next)
    }

    private fun clearComposition() {
        alreadySent = 0
        replaceComposition("")
    }

    /** Sends whatever part of the composition has not gone out yet. */
    private fun flushPending() {
        if (composing.length > alreadySent) {
            sendText(composing.substring(alreadySent))
            alreadySent = composing.length
        }
    }

    private fun sendText(text: String) {
        if (text.isEmpty()) return
        send(text.toByteArray(Charsets.UTF_8))
        armDedupGuard(text)
    }

    private fun send(bytes: ByteArray) {
        if (bytes.isEmpty()) return
        sink.send(bytes)
    }

    private fun armDedupGuard(sent: String) {
        dedupQueue.clear()
        sent.forEach(dedupQueue::addLast)
        dedupDeadlineNanos = nowNanos() + DEDUP_WINDOW_NANOS
    }

    private fun consumeIfDuplicate(char: Char): Boolean {
        if (dedupQueue.isEmpty() || nowNanos() > dedupDeadlineNanos) return false
        if (dedupQueue.first() != char) return false
        dedupQueue.removeFirst()
        return true
    }

    private companion object {
        /**
         * Virtual context sentinels from the Unicode Private Use Area: never in
         * real text, not letters, and never sent to the terminal.
         */
        const val LEFT_SENTINEL = "\uE000"
        const val RIGHT_SENTINEL = "\uE001"

        const val DEL_BYTE: Byte = 0x7f
        const val DEDUP_WINDOW_NANOS = 150_000_000L

        /** How many leading characters two strings have in common. */
        fun commonPrefix(a: CharSequence, b: CharSequence): Int {
            val cutoff = minOf(a.length, b.length)
            var i = 0
            while (i < cutoff && a[i] == b[i]) i++
            return i
        }
    }
}
