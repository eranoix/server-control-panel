package dev.servercontrolpanel.feature.terminal.scroll

import dev.servercontrolpanel.terminalengine.TerminalModes

/**
 * What a vertical drag on the grid MEANS. A pure function, with no Compose and
 * no native engine, so the rule can be read and tested on its own — it is the
 * part of the gesture where it is easy to be silently wrong.
 *
 * The sign is the same all the way up the stack, matching a mouse wheel's
 * delta and the emulator's `scrollViewport`: **negative is towards the past**
 * (upwards), positive is towards the present (downwards).
 */
sealed interface ScrollAction {

    /**
     * Scroll the emulator's **local history**. This is the shell-prompt case:
     * the scrollback lives here, nobody on the other side needs telling, and
     * the response is immediate because it does not depend on the network.
     */
    data class Viewport(val lines: Int) : ScrollAction

    /**
     * Send a **mouse wheel** to the remote program. Once it has asked for
     * tracking (`htop`, `vim` with the mouse, `less`), scrolling locally would
     * be wrong twice over: the program draws its own screen — there is no
     * history of ours to navigate — and it EXPECTS the wheel in order to
     * scroll its own content.
     */
    data class Wheel(val lines: Int) : ScrollAction

    /**
     * Send **arrow keys**, up or down. This is xterm's `alternateScroll`
     * convention (DECSET 1007): on the alternate screen, with no mouse, the
     * wheel becomes an arrow.
     *
     * It is what makes `less`, `man` and a mouse-less `vim` scroll under a
     * finger. Without it the gesture would be inert in precisely the programs
     * one reads the most in.
     */
    data class Arrows(val lines: Int) : ScrollAction

    /**
     * Do nothing — and that is a legitimate answer, not a failure.
     *
     * The alternate screen with 1007 off is the case: there is no history to
     * navigate, and the program has declared it does not want the wheel.
     * Faking movement there would be lying to the owner of the device.
     */
    data object Nothing : ScrollAction
}

/**
 * Decides where a drag of [lines] lines goes, from the emulator's REAL state.
 * No decision comes from a switch in the app — the same discipline the mouse
 * and pasting already follow.
 *
 * The order of the questions is what matters:
 * 1. **Has the program asked for the mouse?** Then the gesture is its own, on
 *    any screen.
 * 2. **Are we on the alternate screen?** Then there is no history here at all:
 *    either it becomes an arrow (1007 on, the default), or it becomes nothing.
 * 3. **Otherwise**, this is the normal screen with scrollback: scroll the
 *    local viewport.
 */
fun decideScroll(modes: TerminalModes, lines: Int): ScrollAction {
    if (lines == 0) return ScrollAction.Nothing

    if (modes.mouseTracking) return ScrollAction.Wheel(lines)

    if (modes.altScreen) {
        return if (modes.altScroll) ScrollAction.Arrows(lines) else ScrollAction.Nothing
    }

    return ScrollAction.Viewport(lines)
}

/** The bytes of an arrow key, repeated [lines] times, respecting DECCKM. */
fun arrowBytes(lines: Int, cursorKeysApplication: Boolean): ByteArray {
    if (lines == 0) return ByteArray(0)
    // ESC [ A / ESC [ B in normal mode; ESC O A / ESC O B in application
    // mode. Sending the wrong form does not make the program scroll — it makes
    // the program receive rubbish.
    val introducer = if (cursorKeysApplication) "\u001bO" else "\u001b["
    val letter = if (lines < 0) "A" else "B"
    val one = (introducer + letter).toByteArray(Charsets.US_ASCII)
    val times = kotlin.math.abs(lines)
    val out = ByteArray(one.size * times)
    for (i in 0 until times) one.copyInto(out, i * one.size)
    return out
}
