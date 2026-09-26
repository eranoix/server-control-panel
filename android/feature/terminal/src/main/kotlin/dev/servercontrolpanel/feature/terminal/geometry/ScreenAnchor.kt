package dev.servercontrolpanel.feature.terminal.geometry

/**
 * Where the terminal frame rests against the visible area.
 *
 * The grid has as many rows as fit on screen, but a TUI frame (e.g. an input
 * composer with a footer) may use far fewer, leaving it stranded at the top, away
 * from the thumb and the keyboard. Unlike a desktop terminal, this grid is born
 * large, filled by a replay, and can grow later, so blank space at the bottom can
 * happen.
 *
 * Rule: the bottom of the content rests against the bottom of the visible area.
 * A shorter frame moves down, a taller one (keyboard up) moves up to show the
 * end, an equal one stays put.
 */
object ScreenAnchor {

    /**
     * How far the frame shifts in Y; positive moves down, negative up.
     *
     * @param contentBottomPx where the useful content ends; see [lastUsefulRow].
     * @param visibleHeightPx the height the user actually sees.
     * @param maxLiftPx how much grid lies beyond the visible area (what the
     *   keyboard covers); lifting further would drag the grid off screen.
     */
    fun offsetY(contentBottomPx: Int, visibleHeightPx: Int, maxLiftPx: Int): Int =
        (visibleHeightPx - contentBottomPx).coerceAtLeast(-maxLiftPx)

    /**
     * The last row that must stay visible: the larger of the last drawn row and
     * the cursor row plus [slackBelowCursor]. The slack matters because TUIs draw
     * below the cursor (an input box's bottom border); the drawn-content check
     * matters when the cursor sits on a blank line above a footer.
     */
    fun lastUsefulRow(lastRowWithContent: Int, cursorRow: Int, slackBelowCursor: Int, lines: Int): Int {
        // An empty grid has nothing to anchor to. Falling back to the cursor
        // (row 0) would shrink the frame into a two-line strip at the bottom, so
        // leave it and let content fill from the top.
        if (lastRowWithContent < 0) return (lines - 1).coerceAtLeast(0)
        val target = maxOf(lastRowWithContent, cursorRow + slackBelowCursor)
        return target.coerceIn(0, (lines - 1).coerceAtLeast(0))
    }

    /**
     * The last row with anything drawn, scanning bottom up (on a full screen the
     * first row checked answers). Returns `-1` for an empty grid, which callers
     * treat as "nothing to anchor".
     *
     * @param isEmpty whether cell `(x, y)` draws nothing. A space counts as empty on
     *   purpose, so trailing spaces do not extend a row.
     */
    inline fun lastRowWithContent(columns: Int, lines: Int, isEmpty: (Int, Int) -> Boolean): Int {
        for (y in lines - 1 downTo 0) {
            for (x in 0 until columns) {
                if (!isEmpty(x, y)) return y
            }
        }
        return -1
    }
}
