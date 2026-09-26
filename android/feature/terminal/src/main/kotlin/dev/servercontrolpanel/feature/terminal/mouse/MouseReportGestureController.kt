package dev.servercontrolpanel.feature.terminal.mouse

import androidx.compose.ui.geometry.Offset
import dev.servercontrolpanel.feature.terminal.input.ByteSink
import dev.servercontrolpanel.feature.terminal.selection.CanvasDragTarget
import dev.servercontrolpanel.feature.terminal.selection.CanvasTapTarget
import dev.servercontrolpanel.feature.terminal.selection.DragPhase
import dev.servercontrolpanel.terminalengine.MouseAction
import dev.servercontrolpanel.terminalengine.MouseButton

/**
 * Translates a touch event into the sequence the remote program expects — **or
 * into nothing**, which is the normal case.
 *
 * Returning `null` is not a failure: it is the VT emulator answering "this event
 * has no recipient". It happens when the program has not asked for mouse
 * tracking (a `bash` prompt, all of the time) and when the finger moved without
 * leaving the cell.
 *
 * This interface exists so that [MouseReportGestureController] stays testable on
 * the JVM: the production implementation is a call into `TerminalEngine`, which
 * loads the native `.so` and only runs on a device.
 * The correctness of the encoding itself — format, coordinates, modes — is
 * proved against the real libghostty-vt in
 * `:terminal-engine`'s `MousePasteEncodingTest`.
 */
fun interface MouseEventEncoder {
    fun encode(
        action: MouseAction,
        position: Offset,
        button: MouseButton,
        anyButtonPressed: Boolean,
    ): ByteArray?
}

/**
 * Sends the mouse events of a touch gesture to the remote program, through
 * [sink] ([ByteSink] — a mouse event is ordinary terminal input, never a paste).
 *
 * **What changed and why.** This controller used to write the SGR sequence by
 * hand (`CSI < Cb ; Cx ; Cy M`) and send it whenever the manual switch was on.
 * Two defects in one stroke:
 *
 * 1. **No recipient.** At a shell prompt nobody asked for the mouse, so the
 *    bytes arrived as literal text on the command line.
 * 2. **Guessed format.** SGR (1006) was emitted even for a program that had only
 *    enabled the X10 format — which does not understand SGR.
 *
 * Encoding is now done by the VT emulator, which knows the mode and the format
 * the program asked for. This controller only decides WHICH event each phase of
 * the gesture represents, and only sends back whatever comes out non-empty.
 */
class MouseReportGestureController(
    private val encoder: MouseEventEncoder,
    private val sink: ByteSink,
    private val button: MouseButton = MouseButton.LEFT,
) : CanvasDragTarget, CanvasTapTarget {

    /**
     * A short tap is a CLICK: press and release in the same cell, which is the
     * pair of events an `htop` or a `vim` expects in order to handle "they
     * clicked here".
     *
     * [taps] is ignored on purpose: two quick taps in a full-screen program are
     * two clicks, not a word-selection gesture — double-tap selection belongs to
     * the app, and the app does not own the gesture in this mode.
     */
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

    /**
     * The last MOVEMENT report sent. A dragging finger produces one touch event
     * per frame — dozens of them within the same cell — and each would become a
     * WebSocket frame and a write to the PTY. Since the encoded sequence carries
     * the cell, two identical reports in a row say NOTHING new to the remote
     * program: it already knows the pointer is there.
     *
     * The deduplication lives here rather than in the native encoder's
     * `TRACK_LAST_CELL` option: measured on the emulator, that option does not
     * suppress repeated movement in button-tracking mode (1002), which is
     * precisely what a finger drag uses.
     */
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
