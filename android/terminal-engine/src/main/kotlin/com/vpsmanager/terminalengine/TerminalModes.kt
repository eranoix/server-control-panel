package com.vpsmanager.terminalengine

/**
 * The terminal modes the INTERFACE needs to know about. None of them is the
 * app's choice: what turns them on and off is the program running on the other
 * side, through DEC sequences, and the emulator (libghostty-vt) tracks them as
 * it processes the PTY's output. This class only carries that truth up to the
 * gesture layer.
 *
 * Which is why it exists: before, the app decided on its own — it emitted mouse
 * bytes because a manual switch was on, and bracketed every paste just because.
 * In both cases the recipient might not exist, and what was meant to be an
 * event became TEXT on the command line.
 */
data class TerminalModes(
    /**
     * Some mouse tracking is active (DECSET 1000 click, 1002 drag, 1003 any
     * movement, or the old X10, mode 9).
     *
     * False is the normal state at a shell prompt: `bash` does not ask for the
     * mouse. While it is false, NO mouse byte has a recipient.
     */
    val mouseTracking: Boolean,

    /**
     * Bracketed paste (DECSET 2004) is active: pasted text has to be wrapped in
     * `ESC[200~` … `ESC[201~` for the program to treat it as a block rather
     * than as typing. With it off, the markers are literal rubbish — hence the
     * decision can never be "always wrap".
     */
    val bracketedPaste: Boolean,

    /**
     * The **alternate screen** is active — `vim`, `htop` full-screen, `less`.
     * It is a frame the size of the window and has **no history at all**:
     * nothing scrolls out of it to be kept.
     *
     * That is why it changes what a vertical drag means. Scrolling here cannot
     * navigate a scrollback that does not exist — libghostty-vt itself pins the
     * viewport to the active area on this screen.
     */
    val altScreen: Boolean = false,

    /**
     * **Alternate scroll** (DECSET 1007), xterm's `alternateScroll` convention:
     * on the alternate screen, and only when the program has not asked for the
     * mouse, the wheel becomes **up/down arrow**.
     *
     * It is what makes `less`, `man` and a mouse-less `vim` scroll under your
     * finger instead of sitting inert — the program receives exactly what it
     * would receive from a mouse wheel on a desktop terminal.
     *
     * **The default is `true`, and that is not a mistake.** 1007 is born ON in
     * a freshly created terminal — that is xterm's convention, and
     * `ViewportScrollTest` measures it against the real library. It matters
     * because `less` and `man` enter the alternate screen but do NOT turn 1007
     * on themselves: the terminal is what turns it on. A `false` default here
     * would leave scrolling inert in exactly the programs people read most in.
     */
    val altScroll: Boolean = true,

    /**
     * Cursor keys in **application** mode (DECCKM, DECSET 1): an arrow goes out
     * as `ESC O A` instead of `ESC [ A`.
     *
     * It only matters for the [altScroll] path: sending the wrong form does not
     * make the program scroll, it makes it receive rubbish.
     */
    val cursorKeysApplication: Boolean = false,
) {
    companion object {
        /**
         * What a freshly created terminal reports, and the safe default when
         * there is no engine.
         *
         * Note it is NOT "everything off": [altScroll] is born on, and this
         * value describes a real terminal rather than a convenient zero. With
         * no engine, [altScreen] is false and 1007 changes no decision at all.
         */
        val NENHUM = TerminalModes(mouseTracking = false, bracketedPaste = false)

        internal const val BIT_MOUSE_TRACKING = 1
        internal const val BIT_BRACKETED_PASTE = 2
        internal const val BIT_ALT_SCREEN = 4
        internal const val BIT_ALT_SCROLL = 8
        internal const val BIT_CURSOR_KEYS_APP = 16

        internal fun fromBits(bits: Int): TerminalModes = TerminalModes(
            mouseTracking = (bits and BIT_MOUSE_TRACKING) != 0,
            bracketedPaste = (bits and BIT_BRACKETED_PASTE) != 0,
            altScreen = (bits and BIT_ALT_SCREEN) != 0,
            altScroll = (bits and BIT_ALT_SCROLL) != 0,
            cursorKeysApplication = (bits and BIT_CURSOR_KEYS_APP) != 0,
        )
    }
}

/**
 * A mouse event's action. Mirrors `GhosttyMouseAction` (vt/mouse/event.h) —
 * only the integer crosses the JNI boundary, so the enum lives here instead of
 * duplicating the native header.
 */
enum class MouseAction(internal val nativeValue: Int) {
    PRESS(0),
    RELEASE(1),
    MOTION(2),
}

/** Mirrors `GhosttyMouseButton`. `NENHUM` is the "no button" of free movement. */
enum class MouseButton(internal val nativeValue: Int) {
    NENHUM(0),
    ESQUERDO(1),
    DIREITO(2),
    MEIO(3),

    /**
     * Wheel up. Not our invention: since xterm, the wheel **is** button 4 (and
     * 5 for down), sent as a PRESS with no RELEASE. That is how `htop`, `vim`
     * and `less` recognise the wheel — any other encoding is simply not read as
     * scrolling.
     */
    RODA_CIMA(4),

    /** Wheel down — button 5 of the same xterm convention. */
    RODA_BAIXO(5),
}

/**
 * The rendered geometry that turns the finger's position, in pixels, into the
 * cell the remote program will receive. The same numbers the grid uses to draw
 * — passed in from outside, never inferred by the engine.
 */
data class MouseGeometry(
    val cellWidthPx: Int,
    val cellHeightPx: Int,
    val screenWidthPx: Int,
    val screenHeightPx: Int,
)
