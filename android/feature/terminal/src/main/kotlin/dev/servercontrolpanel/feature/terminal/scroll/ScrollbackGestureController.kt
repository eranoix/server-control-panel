package dev.servercontrolpanel.feature.terminal.scroll

import androidx.compose.ui.geometry.Offset
import dev.servercontrolpanel.terminalengine.MouseAction
import dev.servercontrolpanel.terminalengine.MouseButton
import dev.servercontrolpanel.terminalengine.MouseGeometry
import dev.servercontrolpanel.terminalengine.TerminalModes
import kotlin.math.abs

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

        val lines = -wholeRows

        return when (val action = decideScroll(modes(), lines)) {
            is ScrollAction.Viewport -> {
                scrollViewport(action.lines)
                canScrollViewport(action.lines)
            }

            is ScrollAction.Wheel -> {
                sendWheel(action.lines, position, geo)
                true
            }

            is ScrollAction.Arrows -> {
                sendBytes(arrowBytes(action.lines, modes().cursorKeysApplication))
                true
            }

            ScrollAction.Nothing -> false
        }
    }

    private fun sendWheel(lines: Int, position: Offset, geo: MouseGeometry) {
        val button = if (lines < 0) MouseButton.WHEEL_UP else MouseButton.WHEEL_DOWN
        repeat(abs(lines).coerceAtMost(MAX_WHEEL_PER_EVENT)) {
            encodeMouse(MouseAction.PRESS, button, position.x, position.y, geo)
                ?.let(sendBytes)
        }
    }

    private companion object {
        const val MAX_WHEEL_PER_EVENT = 10
    }
}
