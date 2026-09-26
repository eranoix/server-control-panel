package com.vpsmanager.feature.terminal.scroll

import com.vpsmanager.terminalengine.TerminalModes

/**
 * What a vertical drag on the grid MEANS. A pure function, with no Compose and
 * no native engine, so the rule can be read and tested on its own — it is the
 * part of the gesture where it is easy to be silently wrong.
 *
 * The sign is the same all the way up the stack, matching a mouse wheel's
 * delta and the emulator's `scrollViewport`: **negative is towards the past**
 * (upwards), positive is towards the present (downwards).
 */
sealed interface AcaoDeRolagem {

    /**
     * Scroll the emulator's **local history**. This is the shell-prompt case:
     * the scrollback lives here, nobody on the other side needs telling, and
     * the response is immediate because it does not depend on the network.
     */
    data class Viewport(val linhas: Int) : AcaoDeRolagem

    /**
     * Send a **mouse wheel** to the remote program. Once it has asked for
     * tracking (`htop`, `vim` with the mouse, `less`), scrolling locally would
     * be wrong twice over: the program draws its own screen — there is no
     * history of ours to navigate — and it EXPECTS the wheel in order to
     * scroll its own content.
     */
    data class Roda(val linhas: Int) : AcaoDeRolagem

    /**
     * Send **arrow keys**, up or down. This is xterm's `alternateScroll`
     * convention (DECSET 1007): on the alternate screen, with no mouse, the
     * wheel becomes an arrow.
     *
     * It is what makes `less`, `man` and a mouse-less `vim` scroll under a
     * finger. Without it the gesture would be inert in precisely the programs
     * one reads the most in.
     */
    data class Setas(val linhas: Int) : AcaoDeRolagem

    /**
     * Do nothing — and that is a legitimate answer, not a failure.
     *
     * The alternate screen with 1007 off is the case: there is no history to
     * navigate, and the program has declared it does not want the wheel.
     * Faking movement there would be lying to the owner of the device.
     */
    data object Nada : AcaoDeRolagem
}

/**
 * Decides where a drag of [linhas] lines goes, from the emulator's REAL state.
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
fun decidirRolagem(modes: TerminalModes, linhas: Int): AcaoDeRolagem {
    if (linhas == 0) return AcaoDeRolagem.Nada

    if (modes.mouseTracking) return AcaoDeRolagem.Roda(linhas)

    if (modes.altScreen) {
        return if (modes.altScroll) AcaoDeRolagem.Setas(linhas) else AcaoDeRolagem.Nada
    }

    return AcaoDeRolagem.Viewport(linhas)
}

/** The bytes of an arrow key, repeated [linhas] times, respecting DECCKM. */
fun bytesDeSeta(linhas: Int, cursorKeysApplication: Boolean): ByteArray {
    if (linhas == 0) return ByteArray(0)
    // ESC [ A / ESC [ B in normal mode; ESC O A / ESC O B in application
    // mode. Sending the wrong form does not make the program scroll — it makes
    // the program receive rubbish.
    val introducer = if (cursorKeysApplication) "\u001bO" else "\u001b["
    val letra = if (linhas < 0) "A" else "B"
    val uma = (introducer + letra).toByteArray(Charsets.US_ASCII)
    val vezes = kotlin.math.abs(linhas)
    val out = ByteArray(uma.size * vezes)
    for (i in 0 until vezes) uma.copyInto(out, i * uma.size)
    return out
}
