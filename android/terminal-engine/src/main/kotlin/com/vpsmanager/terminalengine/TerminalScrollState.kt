package com.vpsmanager.terminalengine

/**
 * Where the viewport sits inside the emulator's history — the position the UI
 * shows and the one that decides whether "back to the end" needs to appear.
 *
 * It mirrors libghostty-vt's `GhosttyTerminalScrollbar`. All three measures
 * are in LINES and in the same space as [TerminalEngine.scrollToRow], so a
 * position read here goes back to the engine with no conversion at all.
 *
 * The library states explicitly that **there is no notification** of a scroll
 * change: whoever draws the position reads this once per frame and compares
 * it with the previous one. That is why this type is a cheap, immutable
 * snapshot and not a flow.
 */
data class TerminalScrollState(
    /** Total scrollable lines: the whole history plus the live screen. */
    val total: Long,

    /** First visible line, counted from the top of the history. */
    val offset: Long,

    /** Quantas linhas cabem na tela. */
    val visiveis: Long,

    /**
     * The viewport is pinned to the end (the active area), i.e. following new
     * output.
     *
     * False is exactly the moment the owner is reading the past — and the
     * moment the screen **must not** jump down by itself because new output
     * arrived.
     */
    val noFim: Boolean,
) {
    /**
     * How many history lines exist above the live screen. Zero when there is
     * nowhere to scroll to — the alternate screen, or a freshly opened session.
     */
    val historico: Long get() = (total - visiveis).coerceAtLeast(0)

    /** There is somewhere to scroll: only then do the position bar and the gesture make sense. */
    val podeRolar: Boolean get() = historico > 0

    /**
     * Progress from 0 (top of the history) to 1 (the end, the live screen),
     * for drawing the bar. With no history the value is 1 — everything is in
     * view, and the end is where you are.
     */
    val progresso: Float
        get() {
            val h = historico
            if (h <= 0) return 1f
            return (offset.toFloat() / h.toFloat()).coerceIn(0f, 1f)
        }

    companion object {
        /** Pinned to the end, with no history: the state of a freshly created terminal. */
        val NO_FIM = TerminalScrollState(total = 0, offset = 0, visiveis = 0, noFim = true)
    }
}
