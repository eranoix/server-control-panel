package com.vpsmanager.feature.terminal.ui

import com.vpsmanager.terminalengine.CellSnapshot
import com.vpsmanager.terminalengine.MouseAction
import com.vpsmanager.terminalengine.MouseButton
import com.vpsmanager.terminalengine.MouseGeometry
import com.vpsmanager.terminalengine.TerminalEngine
import com.vpsmanager.terminalengine.TerminalModes
import com.vpsmanager.terminalengine.TerminalScrollState

/**
 * The narrow slice of [TerminalEngine] [TerminalViewModel] depends on. Kept
 * as its own interface purely so `TerminalViewModel` can be unit-tested on
 * the host JVM: [TerminalEngine] loads a bionic `.so` in its `init` block and
 * cannot be constructed outside an instrumented test (Robolectric has no
 * shadow for it either — see `terminal-engine`'s own androidTest). Production
 * code always uses [RealGridEngine]; tests supply a fake that never touches
 * native code.
 */
internal interface GridEngine {
    fun write(bytes: ByteArray)
    fun snapshot(): CellSnapshot
    fun resize(cols: Int, rows: Int)
    fun close()

    /**
     * The modes the REMOTE PROGRAM has enabled — mouse tracking and bracketed
     * paste. It is part of the contract because the gesture layer needs it in
     * order not to invent behaviour: the absence of this very question was
     * what made the app emit mouse bytes with no recipient.
     */
    fun modes(): TerminalModes

    /** The bytes of a mouse event, or `null` when there is nothing to send. */
    fun encodeMouse(
        action: MouseAction,
        button: MouseButton,
        positionXPx: Float,
        positionYPx: Float,
        geometry: MouseGeometry,
        anyButtonPressed: Boolean,
    ): ByteArray?

    /** Pasted text already encoded for the PTY, bracketed or not according to DECSET 2004. */
    fun encodePaste(text: String): ByteArray

    /**
     * Moves the viewport over the emulator's history — negative goes up (into
     * the past). It is part of the contract because it is the only way through
     * to the scrollback that libghostty-vt has always kept and that nothing
     * exposed.
     */
    fun scrollViewport(linhas: Int)

    /** Pins the viewport back at the end (the live area). */
    fun scrollToBottom()

    /** Where the viewport sits in the history — feeds the position bar. */
    fun scrollState(): TerminalScrollState

    /**
     * Erases the stored history, preserving the live screen.
     *
     * It is part of the contract because it is the ONLY way to undo the copies
     * the attach repaint leaves behind: the remote program cannot reach the
     * scrollback with `ESC[nA`, but the emulator can. See
     * [TerminalEngine.limparHistorico].
     */
    fun limparHistorico()
}

/** Thin adapter over the real native-backed [TerminalEngine]. */
internal class RealGridEngine(private val engine: TerminalEngine) : GridEngine {
    override fun write(bytes: ByteArray) = engine.write(bytes)
    override fun snapshot(): CellSnapshot = engine.snapshot()
    override fun resize(cols: Int, rows: Int) = engine.resize(cols, rows)
    override fun close() = engine.close()
    override fun modes(): TerminalModes = engine.modes()
    override fun encodeMouse(
        action: MouseAction,
        button: MouseButton,
        positionXPx: Float,
        positionYPx: Float,
        geometry: MouseGeometry,
        anyButtonPressed: Boolean,
    ): ByteArray? = engine.encodeMouse(
        action = action,
        button = button,
        positionXPx = positionXPx,
        positionYPx = positionYPx,
        geometry = geometry,
        anyButtonPressed = anyButtonPressed,
    )
    override fun encodePaste(text: String): ByteArray = engine.encodePaste(text)
    override fun scrollViewport(linhas: Int) = engine.scrollViewport(linhas)
    override fun scrollToBottom() = engine.scrollToBottom()
    override fun scrollState(): TerminalScrollState = engine.scrollState()
    override fun limparHistorico() = engine.limparHistorico()

    companion object {
        fun create(cols: Int, rows: Int, scrollback: Int): GridEngine =
            RealGridEngine(TerminalEngine.create(cols, rows, scrollback))
    }
}
