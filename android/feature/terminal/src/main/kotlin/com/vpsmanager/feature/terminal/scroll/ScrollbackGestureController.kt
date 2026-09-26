package com.vpsmanager.feature.terminal.scroll

import androidx.compose.ui.geometry.Offset
import com.vpsmanager.terminalengine.MouseAction
import com.vpsmanager.terminalengine.MouseButton
import com.vpsmanager.terminalengine.MouseGeometry
import com.vpsmanager.terminalengine.TerminalModes
import kotlin.math.abs

/**
 * Translates the vertical drag, which arrives in PIXELS, into what the
 * terminal understands, which is ROWS — and sends each row to the destination
 * [decideScroll] chose.
 *
 * It sits outside Compose on purpose: that way the rule for "how many pixels
 * become how many rows, and where they go" is testable on the JVM, with no
 * device.
 *
 * The accumulator is the detail that makes the scrolling feel natural. A
 * finger drag produces dozens of events of a few pixels each; rounding each
 * one to a whole row would make the screen jump three rows at a time or never
 * move at all. Here the remainder between events is kept, and the content
 * follows the finger cell by cell.
 */
internal class ScrollbackGestureController(
    private val modes: () -> TerminalModes,
    private val geometry: () -> MouseGeometry?,
    private val scrollViewport: (Int) -> Unit,
    private val canScrollViewport: (Int) -> Boolean,
    private val sendBytes: (ByteArray) -> Unit,
    private val encodeMouse: (
        action: MouseAction,
        button: MouseButton,
        xPx: Float,
        yPx: Float,
        geometry: MouseGeometry,
    ) -> ByteArray?,
) : CanvasScrollTarget {

    /** Leftover pixels that have not yet added up to a row. */
    private var accumulatedPx = 0f

    override fun onScrollStart() {
        accumulatedPx = 0f
    }

    override fun onScrollEnd() {
        accumulatedPx = 0f
    }

    override fun onScroll(deltaPx: Float, position: Offset): Boolean {
        val geo = geometry() ?: return false
        val cellHeight = geo.cellHeightPx
        if (cellHeight <= 0) return false

        accumulatedPx += deltaPx
        val wholeRows = (accumulatedPx / cellHeight).toInt()
        if (wholeRows == 0) return true
        accumulatedPx -= wholeRows * cellHeight

        // The finger moves down, the content shows the PAST. By the
        // convention used throughout the stack (and by the mouse wheel), the
        // past is negative.
        val lines = -wholeRows

        return when (val action = decideScroll(modes(), lines)) {
            is ScrollAction.Viewport -> {
                scrollViewport(action.lines)
                canScrollViewport(action.lines)
            }

            is ScrollAction.Wheel -> {
                sendWheel(action.lines, position, geo)
                // The wheel belongs to the remote program: there is no end
                // of scrollback of ours to reach, so the fling is never
                // interrupted here.
                true
            }

            is ScrollAction.Arrows -> {
                sendBytes(arrowBytes(action.lines, modes().cursorKeysApplication))
                true
            }

            ScrollAction.Nothing -> false
        }
    }

    /**
     * One wheel event per row. It is not waste: it is literally what a desk
     * mouse produces, and it is how `htop` and `vim` count how far to scroll.
     */
    private fun sendWheel(lines: Int, position: Offset, geo: MouseGeometry) {
        val button = if (lines < 0) MouseButton.WHEEL_UP else MouseButton.WHEEL_DOWN
        repeat(abs(lines).coerceAtMost(MAX_WHEEL_PER_EVENT)) {
            // The wheel is a PRESS with no RELEASE — the xterm convention
            // from the very beginning. Sending a RELEASE along makes some
            // programs count two scrolls.
            encodeMouse(MouseAction.PRESS, button, position.x, position.y, geo)
                ?.let(sendBytes)
        }
    }

    private companion object {
        /**
         * A cap per event. An absurdly fast drag must not turn into hundreds
         * of wheel events at once on the PTY.
         */
        const val MAX_WHEEL_PER_EVENT = 10
    }
}
