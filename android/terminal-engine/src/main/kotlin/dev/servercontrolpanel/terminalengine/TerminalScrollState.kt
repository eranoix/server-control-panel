package dev.servercontrolpanel.terminalengine

/**
 * Where the viewport sits inside the emulator's history, mirroring
 * libghostty-vt's `GhosttyTerminalScrollbar`. Measures are in LINES, in the
 * same space as [TerminalEngine.scrollToRow].
 *
 * A cheap immutable snapshot rather than a flow, because the library has no
 * scroll-change notification and the UI polls it once per frame.
 */
data class TerminalScrollState(
    /** Total scrollable lines: the whole history plus the live screen. */
    val total: Long,

    /** First visible line, counted from the top of the history. */
    val offset: Long,

    /** How many lines fit on the screen. */
    val visible: Long,

    /**
     * The viewport is pinned to the end (the active area), following new
     * output. While false the user is reading the past and the screen must
     * not jump down on new output.
     */
    val atEnd: Boolean,
) {
    /**
     * History lines above the live screen. Zero on the alternate screen or in
     * a freshly opened session.
     */
    val history: Long get() = (total - visible).coerceAtLeast(0)

    /** Whether there is anywhere to scroll. */
    val canScroll: Boolean get() = history > 0

    /**
     * Progress from 0 (top of the history) to 1 (the live screen), for drawing
     * the bar. 1 when there is no history.
     */
    val progress: Float
        get() {
            val h = history
            if (h <= 0) return 1f
            return (offset.toFloat() / h.toFloat()).coerceIn(0f, 1f)
        }

    companion object {
        /** Pinned to the end, with no history: the state of a freshly created terminal. */
        val AT_END = TerminalScrollState(total = 0, offset = 0, visible = 0, atEnd = true)
    }
}
