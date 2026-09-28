package dev.servercontrolpanel.feature.terminal.scroll

import dev.servercontrolpanel.terminalengine.TerminalModes

sealed interface ScrollAction {

    data class Viewport(val lines: Int) : ScrollAction

    data class Wheel(val lines: Int) : ScrollAction

    data class Arrows(val lines: Int) : ScrollAction

    data object Nothing : ScrollAction
}

fun decideScroll(modes: TerminalModes, lines: Int): ScrollAction {
    if (lines == 0) return ScrollAction.Nothing

    if (modes.mouseTracking) return ScrollAction.Wheel(lines)

    if (modes.altScreen) {
        return if (modes.altScroll) ScrollAction.Arrows(lines) else ScrollAction.Nothing
    }

    return ScrollAction.Viewport(lines)
}

fun arrowBytes(lines: Int, cursorKeysApplication: Boolean): ByteArray {
    if (lines == 0) return ByteArray(0)
    val introducer = if (cursorKeysApplication) "\u001bO" else "\u001b["
    val letter = if (lines < 0) "A" else "B"
    val one = (introducer + letter).toByteArray(Charsets.US_ASCII)
    val times = kotlin.math.abs(lines)
    val out = ByteArray(one.size * times)
    for (i in 0 until times) one.copyInto(out, i * one.size)
    return out
}
