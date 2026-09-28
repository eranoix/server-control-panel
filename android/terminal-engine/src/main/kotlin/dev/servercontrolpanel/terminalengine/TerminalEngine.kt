package dev.servercontrolpanel.terminalengine

import android.view.KeyEvent
import java.nio.ByteBuffer
import java.nio.ByteOrder

class TerminalEngine private constructor(initialCols: Int, initialRows: Int, scrollback: Int) {

    private class Viewport(val buffer: ByteBuffer, val cols: Int, val rows: Int)

    private val handle: Long = nativeCreate(initialCols, initialRows, scrollback)

    private val viewportLock = Any()

    @Volatile private var viewport: Viewport
    @Volatile private var closed = false

    init {
        check(handle != 0L) { "native ghostty terminal allocation failed" }
        viewport = Viewport(
            nativeBuffer(handle).order(ByteOrder.LITTLE_ENDIAN),
            initialCols,
            initialRows,
        )
    }

    fun write(data: ByteArray) {
        checkOpen()
        nativeWrite(handle, data)
    }

    fun snapshot(): CellSnapshot = synchronized(viewportLock) {
        checkOpen()
        nativeSnapshot(handle)
        val current = viewport
        CellSnapshot.fromBuffer(current.buffer, current.cols, current.rows)
    }

    fun resize(newCols: Int, newRows: Int) = synchronized(viewportLock) {
        checkOpen()
        viewport = Viewport(
            nativeResize(handle, newCols, newRows).order(ByteOrder.LITTLE_ENDIAN),
            newCols,
            newRows,
        )
    }

    fun encodeKey(event: KeyEvent, cursorMode: KeyByteEncoder.CursorMode = KeyByteEncoder.CursorMode.NORMAL): ByteArray? {
        checkOpen()
        val unicode = event.unicodeChar
        val utf8 = if (unicode != 0 && unicode !in 0x00..0x1f && unicode != 0x7f) {
            String(Character.toChars(unicode)).toByteArray(Charsets.UTF_8)
        } else {
            null
        }
        val encoded = nativeEncodeKey(
            handle,
            androidActionToGhostty(event),
            event.keyCode,
            androidModsToGhostty(event),
            event.unicodeChar,
            utf8,
            cursorMode == KeyByteEncoder.CursorMode.APPLICATION,
            false,
        )
        if (encoded != null) return encoded

        if (event.isCtrlPressed && event.keyCode in KeyEvent.KEYCODE_A..KeyEvent.KEYCODE_Z) {
            return KeyByteEncoder.encode(event, cursorMode)
        }
        return null
    }

    fun modes(): TerminalModes {
        checkOpen()
        return TerminalModes.fromBits(nativeModes(handle))
    }

    fun encodeMouse(
        action: MouseAction,
        button: MouseButton,
        positionXPx: Float,
        positionYPx: Float,
        geometry: MouseGeometry,
        mods: Int = 0,
        anyButtonPressed: Boolean = false,
    ): ByteArray? {
        checkOpen()
        return nativeEncodeMouse(
            handle,
            action.nativeValue,
            button.nativeValue,
            mods,
            positionXPx,
            positionYPx,
            geometry.cellWidthPx,
            geometry.cellHeightPx,
            geometry.screenWidthPx,
            geometry.screenHeightPx,
            anyButtonPressed,
        )
    }

    fun scrollViewport(lines: Int) {
        checkOpen()
        if (lines == 0) return
        nativeScrollViewport(handle, TAG_SCROLL_DELTA, lines.toLong())
    }

    fun clearHistory() {
        checkOpen()
        nativeWrite(handle, ERASE_SAVED_LINES)
    }

    fun scrollToBottom() {
        checkOpen()
        nativeScrollViewport(handle, TAG_SCROLL_BOTTOM, 0L)
    }

    fun scrollToTop() {
        checkOpen()
        nativeScrollViewport(handle, TAG_SCROLL_TOP, 0L)
    }

    fun scrollToRow(line: Long) {
        checkOpen()
        nativeScrollViewport(handle, TAG_SCROLL_ROW, if (line < 0) 0L else line)
    }

    fun scrollState(): TerminalScrollState {
        checkOpen()
        val v = nativeScrollState(handle) ?: return TerminalScrollState.AT_END
        if (v.size < 4) return TerminalScrollState.AT_END
        return TerminalScrollState(
            total = v[0],
            offset = v[1],
            visible = v[2],
            atEnd = v[3] != 0L,
        )
    }

    fun encodePaste(text: String): ByteArray {
        checkOpen()
        return nativeEncodePaste(handle, text.toByteArray(Charsets.UTF_8)) ?: ByteArray(0)
    }

    internal fun debugBufferAllocationCount(): Int {
        checkOpen()
        return nativeDebugBufferAllocationCount(handle)
    }

    fun close() = synchronized(viewportLock) {
        if (closed) return@synchronized
        closed = true
        nativeClose(handle)
    }

    private fun checkOpen() {
        check(!closed) { "TerminalEngine already closed" }
    }

    private fun androidActionToGhostty(event: KeyEvent): Int = when {
        event.action == KeyEvent.ACTION_UP -> GHOSTTY_KEY_ACTION_RELEASE
        event.action == KeyEvent.ACTION_DOWN && event.repeatCount > 0 -> GHOSTTY_KEY_ACTION_REPEAT
        else -> GHOSTTY_KEY_ACTION_PRESS
    }

    private fun androidModsToGhostty(event: KeyEvent): Int {
        var mods = 0
        if (event.isShiftPressed) mods = mods or GHOSTTY_MODS_SHIFT
        if (event.isCtrlPressed) mods = mods or GHOSTTY_MODS_CTRL
        if (event.isAltPressed) mods = mods or GHOSTTY_MODS_ALT
        if (event.isMetaPressed) mods = mods or GHOSTTY_MODS_SUPER
        if (event.isCapsLockOn) mods = mods or GHOSTTY_MODS_CAPS_LOCK
        if (event.isNumLockOn) mods = mods or GHOSTTY_MODS_NUM_LOCK
        return mods
    }

    companion object {
        init {
            System.loadLibrary("terminal_engine_jni")
        }

        private const val GHOSTTY_KEY_ACTION_RELEASE = 0
        private const val GHOSTTY_KEY_ACTION_PRESS = 1
        private const val GHOSTTY_KEY_ACTION_REPEAT = 2

        private const val GHOSTTY_MODS_SHIFT = 1 shl 0
        private const val GHOSTTY_MODS_CTRL = 1 shl 1
        private const val GHOSTTY_MODS_ALT = 1 shl 2
        private const val GHOSTTY_MODS_SUPER = 1 shl 3
        private const val GHOSTTY_MODS_CAPS_LOCK = 1 shl 4
        private const val GHOSTTY_MODS_NUM_LOCK = 1 shl 5

        private const val TAG_SCROLL_TOP = 0
        private const val TAG_SCROLL_BOTTOM = 1
        private const val TAG_SCROLL_DELTA = 2
        private const val TAG_SCROLL_ROW = 3

        private val ERASE_SAVED_LINES = byteArrayOf(0x1b, '['.code.toByte(), '3'.code.toByte(), 'J'.code.toByte())

        const val DEFAULT_SCROLLBACK = 10_000

        fun create(cols: Int, rows: Int, scrollback: Int = DEFAULT_SCROLLBACK): TerminalEngine =
            TerminalEngine(cols, rows, scrollback)

        @JvmStatic private external fun nativeCreate(cols: Int, rows: Int, scrollback: Int): Long
        @JvmStatic private external fun nativeBuffer(handle: Long): ByteBuffer
        @JvmStatic private external fun nativeWrite(handle: Long, data: ByteArray)
        @JvmStatic private external fun nativeSnapshot(handle: Long)
        @JvmStatic private external fun nativeResize(handle: Long, cols: Int, rows: Int): ByteBuffer
        @JvmStatic private external fun nativeEncodeKey(
            handle: Long,
            action: Int,
            androidKeyCode: Int,
            mods: Int,
            unshiftedCodepoint: Int,
            utf8OrNull: ByteArray?,
            cursorApplicationMode: Boolean,
            altEscPrefix: Boolean,
        ): ByteArray?
        @JvmStatic private external fun nativeModes(handle: Long): Int
        @JvmStatic private external fun nativeScrollViewport(handle: Long, tag: Int, value: Long)
        @JvmStatic private external fun nativeScrollState(handle: Long): LongArray?
        @JvmStatic private external fun nativeEncodeMouse(
            handle: Long,
            action: Int,
            button: Int,
            mods: Int,
            xPx: Float,
            yPx: Float,
            cellWidthPx: Int,
            cellHeightPx: Int,
            screenWidthPx: Int,
            screenHeightPx: Int,
            anyButtonPressed: Boolean,
        ): ByteArray?
        @JvmStatic private external fun nativeEncodePaste(handle: Long, utf8: ByteArray): ByteArray?
        @JvmStatic private external fun nativeClose(handle: Long)
        @JvmStatic private external fun nativeDebugBufferAllocationCount(handle: Long): Int
    }
}
