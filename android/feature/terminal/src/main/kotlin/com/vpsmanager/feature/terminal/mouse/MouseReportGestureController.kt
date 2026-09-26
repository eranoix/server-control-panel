package com.vpsmanager.feature.terminal.mouse

import androidx.compose.ui.geometry.Offset
import com.vpsmanager.feature.terminal.input.ByteSink
import com.vpsmanager.feature.terminal.selection.CanvasDragTarget
import com.vpsmanager.feature.terminal.selection.CanvasTapTarget
import com.vpsmanager.feature.terminal.selection.DragPhase
import com.vpsmanager.terminalengine.MouseAction
import com.vpsmanager.terminalengine.MouseButton

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
    private val button: MouseButton = MouseButton.ESQUERDO,
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
        emitir(MouseAction.PRESS, position, anyButtonPressed = true)
        emitir(MouseAction.RELEASE, position, anyButtonPressed = false)
    }

    override fun onDrag(position: Offset, phase: DragPhase) {
        when (phase) {
            DragPhase.START -> {
                ultimoMovimento = null
                emitir(MouseAction.PRESS, position, anyButtonPressed = true)
            }
            DragPhase.MOVE -> emitir(MouseAction.MOTION, position, anyButtonPressed = true)
            DragPhase.END -> {
                emitir(MouseAction.RELEASE, position, anyButtonPressed = false)
                ultimoMovimento = null
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
    private var ultimoMovimento: ByteArray? = null

    private fun emitir(action: MouseAction, position: Offset, anyButtonPressed: Boolean) {
        val bytes = encoder.encode(action, position, button, anyButtonPressed) ?: return
        if (bytes.isEmpty()) return
        if (action == MouseAction.MOTION) {
            if (bytes.contentEquals(ultimoMovimento)) return
            ultimoMovimento = bytes
        }
        sink.send(bytes)
    }
}
