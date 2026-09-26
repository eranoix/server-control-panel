package com.vpsmanager.terminalengine

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
 * Threading contract: [write] may run concurrently with [snapshot] — that is
 * the whole reason the native side is built in two phases, so the thread
 * pushing PTY bytes never waits on the expensive snapshot copy. [snapshot],
 * [resize] and [close], on the other hand, are mutually exclusive, serialized
 * here by [viewportLock]. The C++-side mutex alone would NOT be enough: it
 * covers only the native fill of the buffer, and the copy into Kotlin objects
 * ([CellSnapshot.fromBuffer]) happens AFTER the native call returns — a
 * concurrent [resize] would `delete[]` the memory that copy is still reading.
 * The buffer/cols/rows trio also has to be read as a single thing (a new
 * buffer with the old cols builds a skewed snapshot), which is why [Viewport]
 * is immutable and swapped in one go.
 */
class TerminalEngine private constructor(initialCols: Int, initialRows: Int, scrollback: Int) {

    /** The native buffer plus the geometry that describes its layout: they only make sense together. */
    private class Viewport(val buffer: ByteBuffer, val cols: Int, val rows: Int)

    private val handle: Long = nativeCreate(initialCols, initialRows, scrollback)

    // Serializes snapshot/resize/close. Resize is rare (a layout change) and
    // snapshot is frequent, so in practice contention here is negligible —
    // what matters is that write() does NOT take part in this lock.
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
     * concurrently with [write] from another thread — the native side
     * briefly locks the terminal only for the phase that touches it, then
     * fills the reused buffer under the buffer's lock (not the terminal's),
     * and this call copies that buffer into Kotlin-owned storage before
     * returning.
     */
    fun snapshot(): CellSnapshot = synchronized(viewportLock) {
        checkOpen()
        nativeSnapshot(handle)
        // The fromBuffer copy has to stay INSIDE the lock: it reads native
        // memory after nativeSnapshot has returned, and that is exactly the
        // window a concurrent resize used to free.
        val current = viewport
        CellSnapshot.fromBuffer(current.buffer, current.cols, current.rows)
    }

    /**
     * Resizes the viewport. The reused snapshot buffer is only reallocated
     * here, when dimensions actually change — never per-snapshot.
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

        // Ctrl+I, Ctrl+M and Ctrl+[ do not come out of the native encoder:
        // libghostty-vt leaves those three out of its C0 table ON PURPOSE (an
        // explicit comment in its ctrlSeq, following fixterms) so that they
        // become CSI u instead. But CSI u depends on the "modify other keys"/
        // Kitty mode, which this app does not enable — under legacy encoding
        // the encoder simply writes no byte at all. And Android delivers
        // unicodeChar == 0 while CTRL is held, so there is no text for its
        // fallback path either. Result: Ctrl+I and Ctrl+M reached the PTY as
        // dead keys. This app's contract is [KeyByteEncoder]'s — ctrl+letter is
        // a C0 byte derived from the keyCode, the same on any layout — so we
        // reuse that table, the single source of truth, instead of duplicating
        // the mapping here.
        if (event.isCtrlPressed && event.keyCode in KeyEvent.KEYCODE_A..KeyEvent.KEYCODE_Z) {
            return KeyByteEncoder.encode(event, cursorMode)
        }
        return null
    }

    /**
     * The modes the REMOTE PROGRAM turned on in the terminal — mouse tracking
     * and bracketed paste. It is the question the app never asked and therefore
     * guessed at: the gesture layer consults this to know whether a touch has a
     * recipient, instead of trusting a manual switch.
     *
     * A cheap call: one JNI crossing and two field reads under the terminal's
     * lock. No grid copy, unlike [snapshot].
     */
    fun modes(): TerminalModes {
        checkOpen()
        return TerminalModes.fromBits(nativeModes(handle))
    }

    /**
     * Encodes a mouse event into the sequence the remote program expects — or
     * returns `null` when there is nothing to send.
     *
     * `null` happens in the two cases the app used to ignore:
     * 1. **The program did not ask for the mouse.** At a `bash` prompt there is
     *    no tracking active, and emitting bytes there only dirties the command
     *    line — which was exactly the "crazy text" being reported.
     * 2. **The movement did not change cell.** The encoder's deduplication
     *    drops the event instead of flooding the PTY on every pixel of a drag.
     *
     * The FORMAT (SGR, X10, URxvt, SGR-pixels) also comes from the terminal and
     * not from a guess: the app used to emit SGR always, even to a program that
     * had only enabled X10.
     *
     * @param position finger position in pixels, in the same frame as the grid
     * @param anyButtonPressed whether any button is held — what separates a
     *   drag (movement WITH a button) from free movement
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
     * Moves the emulator's **viewport** across the history it already keeps.
     *
     * This is what was missing for the history to exist at all: libghostty-vt
     * always kept a scrollback (the `scrollback` argument to [create]), but
     * nothing here exposed a way to look back — [snapshot] returned the live
     * screen forever, and the history was **unreachable**.
     *
     * After moving, the next [snapshot] already draws the past lines: the
     * render state follows the viewport, not the active area.
     *
     * On the alternate screen (`vim`, `htop`) the library itself pins the
     * viewport to the active area — there is no history to navigate — so
     * calling this there is harmless, and the rule need not be duplicated here.
     *
     * @param linhas how many lines to move; **negative goes up** (into the
     *   past), positive goes down, exactly like a mouse wheel delta.
     */
    fun scrollViewport(linhas: Int) {
        checkOpen()
        if (linhas == 0) return
        nativeScrollViewport(handle, TAG_SCROLL_DELTA, linhas.toLong())
    }

    /**
     * Erases the stored **history**, leaving the live screen intact.
     *
     * ## Why this has to exist
     *
     * A differential renderer repaints by moving the cursor up with `ESC[nA`.
     * That movement saturates at the first line of the SCREEN — it **does not
     * reach the scrollback**. So every frame that has already scrolled up is,
     * as far as the program is concerned, impossible to erase: it paints the
     * new frame below and the old copy stays.
     *
     * That is what happens on a fresh attach. The server forces a repaint by
     * nudging the PTY's line count (the "wobble", in `internal/pty/pty.go`) and
     * the app paints **two** frames — one at each size. The first scrolls up
     * and becomes a permanent copy. Measured: `67x52 → 67x26 → 67x52` produced
     * two whole copies of the same text, one below the other.
     *
     * The program cannot clear that. **The emulator can** — and it is the same
     * thing any terminal does on receiving `ESC[3J` (*erase saved lines*,
     * xterm), which is exactly the sequence written here rather than inventing
     * a new native entry point.
     *
     * Calling this DISCARDS history: it only makes sense right after a fresh
     * attach, where what sits in the scrollback is repaint scaffolding and not
     * conversation. On a reconnect the history is legitimate — see
     * `TerminalViewModel`.
     */
    fun limparHistorico() {
        checkOpen()
        nativeWrite(handle, ERASE_SAVED_LINES)
    }

    /** Pins the viewport back at the end (the active area) — the "back to the bottom". */
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
     * Jumps to an absolute history row — the same row space as
     * [TerminalScrollState.offset], so a position read back needs no conversion.
     */
    fun scrollToRow(linha: Long) {
        checkOpen()
        nativeScrollViewport(handle, TAG_SCROLL_ROW, if (linha < 0) 0L else linha)
    }

    /**
     * Where the viewport sits within the history. Feeds the position bar and
     * the decision to show the "back to the bottom" control.
     *
     * A cheap call, like [modes]: one JNI crossing and a few field reads under
     * the terminal's lock, with no grid copy. The library warns that **there is
     * no notification** of a scroll change — whoever draws the position reads
     * this once per frame and compares, and that is what the interface does.
     */
    fun scrollState(): TerminalScrollState {
        checkOpen()
        val v = nativeScrollState(handle) ?: return TerminalScrollState.NO_FIM
        if (v.size < 4) return TerminalScrollState.NO_FIM
        return TerminalScrollState(
            total = v[0],
            offset = v[1],
            visiveis = v[2],
            noFim = v[3] != 0L,
        )
    }

    /**
     * Encodes pasted text on its way to the PTY, wrapping it in
     * `ESC[200~`/`ESC[201~` **if, and only if**, the remote program has turned
     * DECSET 2004 on.
     *
     * Without that wrapper, multi-line text pasted into a shell is EXECUTED
     * line by line the instant it is pasted — correctness and safety in equal
     * measure. With it applied at the wrong time (2004 off), the markers become
     * literal text on the command line, the same defect seen with the mouse:
     * which is why the right answer is never "always wrap".
     *
     * The encoding also neutralises control bytes coming from the clipboard —
     * including an `ESC[201~` embedded in the text, which would close the paste
     * halfway through and turn the remainder into a COMMAND.
     */
    fun encodePaste(text: String): ByteArray {
        checkOpen()
        return nativeEncodePaste(handle, text.toByteArray(Charsets.UTF_8)) ?: ByteArray(0)
    }

    /**
     * Number of times the native reused snapshot buffer has actually been
     * (re)allocated for this instance — 1 right after construction, and
     * only incremented again by a [resize] that changes byte capacity.
     * Exists solely so tests can assert the "exactly one buffer, never
     * per-frame allocation" invariant; production code has no use for it.
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

        // Mirrors GhosttyKeyAction (vt/key/event.h) — kept here rather than
        // duplicating the native header, since only the integer value
        // crosses the JNI boundary.
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

        /**
         * `ESC[3J` — xterm's *erase saved lines*: clears the scrollback and
         * leaves the live screen intact. Written through the parser itself
         * rather than a new native entry point, because it is the standard
         * sequence libghostty-vt already implements. See [limparHistorico].
         */
        private val ERASE_SAVED_LINES = byteArrayOf(0x1b, '['.code.toByte(), '3'.code.toByte(), 'J'.code.toByte())

        /**
         * How many LINES of history the emulator keeps (lines, not bytes:
         * `max_scrollback` in `vt/terminal.h` says "maximum number of lines to
         * keep in scrollback history"). The default covers ordinary use well;
         * anyone who wants more passes another value — see
         * `TerminalScrollbackPreference`.
         */
        const val SCROLLBACK_PADRAO = 10_000

        fun create(cols: Int, rows: Int, scrollback: Int = SCROLLBACK_PADRAO): TerminalEngine =
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
