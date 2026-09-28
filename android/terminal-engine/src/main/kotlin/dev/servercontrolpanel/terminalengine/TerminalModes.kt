package dev.servercontrolpanel.terminalengine

data class TerminalModes(
    val mouseTracking: Boolean,

    val bracketedPaste: Boolean,

    val altScreen: Boolean = false,

    val altScroll: Boolean = true,

    val cursorKeysApplication: Boolean = false,
) {
    companion object {
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

enum class MouseAction(internal val nativeValue: Int) {
    PRESS(0),
    RELEASE(1),
    MOTION(2),
}

enum class MouseButton(internal val nativeValue: Int) {
    NONE(0),
    LEFT(1),
    RIGHT(2),
    MIDDLE(3),

    WHEEL_UP(4),

    WHEEL_DOWN(5),
}

data class MouseGeometry(
    val cellWidthPx: Int,
    val cellHeightPx: Int,
    val screenWidthPx: Int,
    val screenHeightPx: Int,
)
