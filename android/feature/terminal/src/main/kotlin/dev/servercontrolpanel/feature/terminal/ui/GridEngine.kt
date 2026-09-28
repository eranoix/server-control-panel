package dev.servercontrolpanel.feature.terminal.ui

import dev.servercontrolpanel.terminalengine.CellSnapshot
import dev.servercontrolpanel.terminalengine.MouseAction
import dev.servercontrolpanel.terminalengine.MouseButton
import dev.servercontrolpanel.terminalengine.MouseGeometry
import dev.servercontrolpanel.terminalengine.TerminalEngine
import dev.servercontrolpanel.terminalengine.TerminalModes
import dev.servercontrolpanel.terminalengine.TerminalScrollState

internal interface GridEngine {
    fun write(bytes: ByteArray)
    fun snapshot(): CellSnapshot
    fun resize(cols: Int, rows: Int)
    fun close()

    fun modes(): TerminalModes

    fun encodeMouse(
        action: MouseAction,
        button: MouseButton,
        positionXPx: Float,
        positionYPx: Float,
        geometry: MouseGeometry,
        anyButtonPressed: Boolean,
    ): ByteArray?

    fun encodePaste(text: String): ByteArray

    fun scrollViewport(lines: Int)

    fun scrollToBottom()

    fun scrollState(): TerminalScrollState

    fun clearHistory()
}

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
    override fun scrollViewport(lines: Int) = engine.scrollViewport(lines)
    override fun scrollToBottom() = engine.scrollToBottom()
    override fun scrollState(): TerminalScrollState = engine.scrollState()
    override fun clearHistory() = engine.clearHistory()

    companion object {
        fun create(cols: Int, rows: Int, scrollback: Int): GridEngine =
            RealGridEngine(TerminalEngine.create(cols, rows, scrollback))
    }
}
