package dev.servercontrolpanel.terminalengine

import android.view.KeyEvent
import java.nio.ByteBuffer
import java.nio.ByteOrder

/**
 * JVM facade over the native terminal library held by the JNI shim in
 * `src/main/cpp`. `write(bytes)` feeds PTY output in, `snapshot()` reads
 * the current grid out as an immutable [CellSnapshot]; there is no Compose
 * dependency and no networking here.
 *
 * Every instance owns exactly one native `EngineHandle` and exactly one
 * reused direct [ByteBuffer] (see the JNI shim's buffer-layout comment)
 * that the native side copies a snapshot into under its own lock and this
 * class only ever reads afterward. [close] is idempotent; every other call
 * on a closed engine throws [IllegalStateException] (thrown from native code
 * against the same cached exception class).
 *
 * Threading contract: [write] may run concurrently with [snapshot], so the
 * PTY thread never waits on the snapshot copy. [snapshot], [resize] and
 * [close] are mutually exclusive via [viewportLock]: the copy into Kotlin
 * objects ([CellSnapshot.fromBuffer]) happens after the native call returns,
 * and a concurrent [resize] would free the memory it reads. Buffer, cols and
 * rows must be read together, which is why [Viewport] is immutable and
 * swapped in one go.
 */
class TerminalEngine private constructor(initialCols: Int, initialRows: Int, scrollback: Int) {

    /** The native buffer plus the geometry that describes its layout: they only make sense together. */
    private class Viewport(val buffer: ByteBuffer, val cols: Int, val rows: Int)

    private val handle: Long = nativeCreate(initialCols, initialRows, scrollback)

    // Serializes snapshot/resize/close. write() must NOT take this lock.
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

    /** Feeds raw PTY output bytes into the terminal. Never fails; malformed sequences are absorbed upstream. */
    fun write(data: ByteArray) {
        checkOpen()
        nativeWrite(handle, data)
    }

    /**
     * Takes an immutable copy of the current viewport. Safe to call
     * concurrently with [write] from another thread.
     */
    fun snapshot(): CellSnapshot = synchronized(viewportLock) {
        checkOpen()
        nativeSnapshot(handle)
        // The fromBuffer copy must stay INSIDE the lock: it reads native
        // memory that a concurrent resize would free.
        val current = viewport
        CellSnapshot.fromBuffer(current.buffer, current.cols, current.rows)
    }

    /**
     * Resizes the viewport. The reused snapshot buffer is only reallocated
     * here, when dimensions actually change, never per snapshot.
     */
    fun resize(newCols: Int, newRows: Int) = synchronized(viewportLock) {
        checkOpen()
        viewport = Viewport(
            nativeResize(handle, newCols, newRows).order(ByteOrder.LITTLE_ENDIAN),
            newCols,
            newRows,
        )
    }

    /**
     * Encodes a hardware/IME [KeyEvent] into the outbound terminal byte
     * sequence libghostty-vt's key encoder produces for it, or null if the
     * encoder has nothing to send. [cursorMode] mirrors
     * [KeyByteEncoder.CursorMode] so callers can share one notion of DECCKM
     * state across both the Robolectric-testable table and this native path.
     */
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

        // libghostty-vt deliberately leaves Ctrl+I, Ctrl+M and Ctrl+[ out of its
        // C0 table (they become CSI u, which needs a Kitty mode we do not
        // enable), and Android reports unicodeChar == 0 while CTRL is held, so
        // the native encoder emits nothing. Fall back to [KeyByteEncoder], the
        // single source of truth for ctrl+letter C0 bytes.
        if (event.isCtrlPressed && event.keyCode in KeyEvent.KEYCODE_A..KeyEvent.KEYCODE_Z) {
            return KeyByteEncoder.encode(event, cursorMode)
        }
        return null
    }

    /**
     * The modes the REMOTE PROGRAM turned on (mouse tracking, bracketed paste,
     * etc.), so the gesture layer knows whether a touch has a recipient.
     *
     * Cheap: one JNI crossing and a few field reads, no grid copy.
     */
    fun modes(): TerminalModes {
        checkOpen()
        return TerminalModes.fromBits(nativeModes(handle))
    }

    /**
     * Encodes a mouse event into the sequence (and format) the remote program
     * expects, or returns `null` when the program did not ask for the mouse or
     * the movement stayed within the same cell.
     *
     * @param anyButtonPressed whether any button is held, which separates a
     *   drag from free movement
     */
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

    /**
     * Moves the emulator's viewport across the history it already keeps; the
     * next [snapshot] draws from the new position.
     *
     * Harmless on the alternate screen, where the library pins the viewport.
     *
     * @param lines how many lines to move; negative goes up (into the past).
     */
    fun scrollViewport(lines: Int) {
        checkOpen()
        if (lines == 0) return
        nativeScrollViewport(handle, TAG_SCROLL_DELTA, lines.toLong())
    }

    /**
     * Erases the stored history, leaving the live screen intact (`ESC[3J`).
     *
     * On a fresh attach the server forces a repaint by nudging the PTY size,
     * and a differential renderer cannot erase frames that already scrolled
     * into the scrollback (`ESC[nA` stops at the top of the screen), leaving
     * duplicate copies. Only call this right after a fresh attach; on a
     * reconnect the history is real (see `TerminalViewModel`).
     */
    fun clearHistory() {
        checkOpen()
        nativeWrite(handle, ERASE_SAVED_LINES)
    }

    /** Pins the viewport back at the end (the active area). */
    fun scrollToBottom() {
        checkOpen()
        nativeScrollViewport(handle, TAG_SCROLL_BOTTOM, 0L)
    }

    /** Takes the viewport to the top of the history. */
    fun scrollToTop() {
        checkOpen()
        nativeScrollViewport(handle, TAG_SCROLL_TOP, 0L)
    }

    /**
     * Jumps to an absolute history row, in the same row space as
     * [TerminalScrollState.offset], so a position read back needs no conversion.
     */
    fun scrollToRow(line: Long) {
        checkOpen()
        nativeScrollViewport(handle, TAG_SCROLL_ROW, if (line < 0) 0L else line)
    }

    /**
     * Where the viewport sits within the history. Cheap (no grid copy); the
     * library has no scroll-change notification, so the UI polls this per frame.
     */
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

    /**
     * Encodes pasted text on its way to the PTY, wrapping it in
     * `ESC[200~`/`ESC[201~` **if, and only if**, the remote program has turned
     * DECSET 2004 on (otherwise the markers would show up as literal text).
     *
     * Also neutralises control bytes from the clipboard, including an embedded
     * `ESC[201~` that would end the paste early and run the rest as a command.
     */
    fun encodePaste(text: String): ByteArray {
        checkOpen()
        return nativeEncodePaste(handle, text.toByteArray(Charsets.UTF_8)) ?: ByteArray(0)
    }

    /**
     * Number of times the native snapshot buffer has been (re)allocated: 1
     * after construction, incremented only by a [resize] that changes byte
     * capacity. Test-only.
     */
    internal fun debugBufferAllocationCount(): Int {
        checkOpen()
        return nativeDebugBufferAllocationCount(handle)
    }

    /**
     * Idempotent. Safe to call more than once; only the first call frees native resources.
     * Under the same lock as [snapshot]/[resize] because [nativeClose] frees the
     * buffer: closing in the middle of a copy would be a use-after-free.
     */
    fun close() = synchronized(viewportLock) {
        if (closed) return@synchronized
        closed = true
        nativeClose(handle)
    }

    private fun checkOpen() {
        check(!closed) { "TerminalEngine already closed" }
    }

    // A held key repeats as ACTION_DOWN with a non-zero repeatCount (Android
    // never resends ACTION_DOWN with repeatCount == 0); ACTION_MULTIPLE is a
    // deprecated legacy path unrelated to physical repeat and is not used.
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

        // Mirrors GhosttyKeyAction (vt/key/event.h); only the integer crosses JNI.
        private const val GHOSTTY_KEY_ACTION_RELEASE = 0
        private const val GHOSTTY_KEY_ACTION_PRESS = 1
        private const val GHOSTTY_KEY_ACTION_REPEAT = 2

        // Mirrors the GHOSTTY_MODS_* bitmask (vt/key/event.h).
        private const val GHOSTTY_MODS_SHIFT = 1 shl 0
        private const val GHOSTTY_MODS_CTRL = 1 shl 1
        private const val GHOSTTY_MODS_ALT = 1 shl 2
        private const val GHOSTTY_MODS_SUPER = 1 shl 3
        private const val GHOSTTY_MODS_CAPS_LOCK = 1 shl 4
        private const val GHOSTTY_MODS_NUM_LOCK = 1 shl 5

        // Mirror GhosttyTerminalScrollViewportTag (vt/terminal.h). Only the
        // integer crosses the JNI boundary, so the values live here.
        private const val TAG_SCROLL_TOP = 0
        private const val TAG_SCROLL_BOTTOM = 1
        private const val TAG_SCROLL_DELTA = 2
        private const val TAG_SCROLL_ROW = 3

        /** `ESC[3J`, xterm's erase saved lines. See [clearHistory]. */
        private val ERASE_SAVED_LINES = byteArrayOf(0x1b, '['.code.toByte(), '3'.code.toByte(), 'J'.code.toByte())

        /**
         * Scrollback size in LINES, not bytes (`max_scrollback` in
         * `vt/terminal.h`). Overridable via `TerminalScrollbackPreference`.
         */
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
