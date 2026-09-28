package dev.servercontrolpanel.feature.terminal.geometry

object ScreenAnchor {

    fun offsetY(contentBottomPx: Int, visibleHeightPx: Int, maxLiftPx: Int): Int =
        (visibleHeightPx - contentBottomPx).coerceAtLeast(-maxLiftPx)

    fun lastUsefulRow(lastRowWithContent: Int, cursorRow: Int, slackBelowCursor: Int, lines: Int): Int {
        if (lastRowWithContent < 0) return (lines - 1).coerceAtLeast(0)
        val target = maxOf(lastRowWithContent, cursorRow + slackBelowCursor)
        return target.coerceIn(0, (lines - 1).coerceAtLeast(0))
    }

    inline fun lastRowWithContent(columns: Int, lines: Int, isEmpty: (Int, Int) -> Boolean): Int {
        for (y in lines - 1 downTo 0) {
            for (x in 0 until columns) {
                if (!isEmpty(x, y)) return y
            }
        }
        return -1
    }
}
