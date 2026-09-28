package dev.servercontrolpanel.feature.terminal.mouse

import androidx.compose.ui.geometry.Offset
import dev.servercontrolpanel.feature.terminal.input.ByteSink
import dev.servercontrolpanel.feature.terminal.selection.CanvasDragTarget
import dev.servercontrolpanel.feature.terminal.selection.CanvasTapTarget
import dev.servercontrolpanel.feature.terminal.selection.DragPhase
import dev.servercontrolpanel.terminalengine.MouseAction
import dev.servercontrolpanel.terminalengine.MouseButton

fun interface MouseEventEncoder {
    fun encode(
        action: MouseAction,
        position: Offset,
        button: MouseButton,
        anyButtonPressed: Boolean,
    ): ByteArray?
}

class MouseReportGestureController(
    private val encoder: MouseEventEncoder,
    private val sink: ByteSink,
    private val button: MouseButton = MouseButton.LEFT,
) : CanvasDragTarget, CanvasTapTarget {

    override fun onTap(position: Offset, taps: Int) {
        emit(MouseAction.PRESS, position, anyButtonPressed = true)
        emit(MouseAction.RELEASE, position, anyButtonPressed = false)
    }

    override fun onDrag(position: Offset, phase: DragPhase) {
        when (phase) {
            DragPhase.START -> {
                lastMove = null
                emit(MouseAction.PRESS, position, anyButtonPressed = true)
            }
            DragPhase.MOVE -> emit(MouseAction.MOTION, position, anyButtonPressed = true)
            DragPhase.END -> {
                emit(MouseAction.RELEASE, position, anyButtonPressed = false)
                lastMove = null
            }
        }
    }

    private var lastMove: ByteArray? = null

    private fun emit(action: MouseAction, position: Offset, anyButtonPressed: Boolean) {
        val bytes = encoder.encode(action, position, button, anyButtonPressed) ?: return
        if (bytes.isEmpty()) return
        if (action == MouseAction.MOTION) {
            if (bytes.contentEquals(lastMove)) return
            lastMove = bytes
        }
        sink.send(bytes)
    }
}
