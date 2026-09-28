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

class TerminalInputConnection(
    view: View,
    private val sink: ByteSink,
    private val cursorMode: KeyByteEncoder.CursorMode = KeyByteEncoder.CursorMode.NORMAL,
    private val mode: () -> TypingMode = { TypingMode.DEFAULT },
    private val onCompositionChange: (String) -> Unit = {},
    private val nowNanos: () -> Long = System::nanoTime,
) : BaseInputConnection(view, false) {

    private val composing = StringBuilder()

    private var alreadySent = 0

    private val dedupQueue = ArrayDeque<Char>()
    private var dedupDeadlineNanos = Long.MIN_VALUE

    override fun getEditable(): Editable? = null

    override fun setComposingText(text: CharSequence, newCursorPosition: Int): Boolean {
        val next = text.toString()
        if (mode().composesText) {
            replaceComposition(next)
            return true
        }

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
        return true
    }

    override fun finishComposingText(): Boolean {
        flushPending()
        clearComposition()
        return true
    }

    override fun commitText(text: CharSequence, newCursorPosition: Int): Boolean {
        val committed = text.toString()

        if (alreadySent > 0) {
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
            val remove = minOf(beforeLength, composing.length)
            replaceComposition(composing.substring(0, composing.length - remove))
            return true
        }
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
        if (composing.isNotEmpty()) {
            flushPending()
            clearComposition()
        }
        sink.send(bytes)
        return true
    }

    override fun performEditorAction(actionCode: Int): Boolean {
        return true
    }

    override fun getTextBeforeCursor(n: Int, flags: Int): CharSequence {
        if (n <= 0) return ""
        val real = if (mode().composesText) composing.toString() else ""
        val virtual = LEFT_SENTINEL + real
        return if (virtual.length <= n) virtual else virtual.substring(virtual.length - n)
    }

    override fun getTextAfterCursor(n: Int, flags: Int): CharSequence {
        if (n <= 0) return ""
        return if (RIGHT_SENTINEL.length <= n) RIGHT_SENTINEL else RIGHT_SENTINEL.substring(0, n)
    }

    override fun getSelectedText(flags: Int): CharSequence? = null

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

    override fun getCursorCapsMode(reqModes: Int): Int {
        if (!mode().composesText) return 0
        return TextUtils.getCapsMode(composing.toString(), composing.length, reqModes)
    }

    private var openBatches = 0

    override fun beginBatchEdit(): Boolean {
        openBatches++
        return true
    }

    override fun endBatchEdit(): Boolean {
        if (openBatches > 0) openBatches--
        return openBatches > 0
    }

    internal fun composingTextForTest(): String = composing.toString()

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
        const val LEFT_SENTINEL = "\uE000"
        const val RIGHT_SENTINEL = "\uE001"

        const val DEL_BYTE: Byte = 0x7f
        const val DEDUP_WINDOW_NANOS = 150_000_000L

        fun commonPrefix(a: CharSequence, b: CharSequence): Int {
            val cutoff = minOf(a.length, b.length)
            var i = 0
            while (i < cutoff && a[i] == b[i]) i++
            return i
        }
    }
}
