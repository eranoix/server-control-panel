package dev.servercontrolpanel.terminalengine

/**
 * Terminal modes the UI needs to know about. The remote program sets them via
 * DEC sequences and libghostty-vt tracks them; this class carries them to the
 * gesture layer so the app never guesses (guessing turned mouse events and
 * paste markers into text on the command line).
 */
data class TerminalModes(
    /**
     * Some mouse tracking is active (DECSET 1000 click, 1002 drag, 1003 any
     * movement, or X10 mode 9). While false, no mouse byte has a recipient.
     */
    val mouseTracking: Boolean,

    /**
     * Bracketed paste (DECSET 2004) is active: pasted text must be wrapped in
     * `ESC[200~` ... `ESC[201~`. With it off, the markers would be literal text.
     */
    val bracketedPaste: Boolean,

    /**
     * The alternate screen is active (full-screen `vim`, `htop`, `less`). It
     * has no history, so a vertical drag cannot navigate scrollback there.
     */
    val altScreen: Boolean = false,

    /**
     * Alternate scroll (DECSET 1007, xterm's `alternateScroll`): on the
     * alternate screen without mouse tracking, the wheel becomes up/down arrows.
     *
     * Defaults to `true` because 1007 starts ON in a fresh terminal
     * (`ViewportScrollTest` checks this against the real library), and `less`
     * and `man` rely on it without enabling it themselves.
     */
    val altScroll: Boolean = true,

    /**
     * Cursor keys in application mode (DECCKM, DECSET 1): arrows are sent as
     * `ESC O A` instead of `ESC [ A`. Matters for the [altScroll] path.
     */
    val cursorKeysApplication: Boolean = false,
) {
    companion object {
        /**
         * What a freshly created terminal reports, and the safe default when
         * there is no engine. Not "everything off": [altScroll] starts on.
         */
        val NONE = TerminalModes(mouseTracking = false, bracketedPaste = false)

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

/** A mouse event's action. Mirrors `GhosttyMouseAction` (vt/mouse/event.h). */
enum class MouseAction(internal val nativeValue: Int) {
    PRESS(0),
    RELEASE(1),
    MOTION(2),
}

/** Mirrors `GhosttyMouseButton`. `NONE` is the "no button" of free movement. */
enum class MouseButton(internal val nativeValue: Int) {
    NONE(0),
    LEFT(1),
    RIGHT(2),
    MIDDLE(3),

    /**
     * Wheel up. By xterm convention the wheel is button 4 (5 for down), sent
     * as a PRESS with no RELEASE; that is how `htop`, `vim` and `less` read it.
     */
    WHEEL_UP(4),

    /** Wheel down, button 5 of the same xterm convention. */
    WHEEL_DOWN(5),
}

/**
 * Rendered geometry that maps a finger position in pixels to the cell the
 * remote program receives. Supplied by the grid, never inferred by the engine.
 */
data class MouseGeometry(
    val cellWidthPx: Int,
    val cellHeightPx: Int,
    val screenWidthPx: Int,
    val screenHeightPx: Int,
)
