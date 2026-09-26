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
 * [decidirRolagem] chose.
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
    private val modos: () -> TerminalModes,
    private val geometria: () -> MouseGeometry?,
    private val rolarViewport: (Int) -> Unit,
    private val podeRolarViewport: (Int) -> Boolean,
    private val enviarBytes: (ByteArray) -> Unit,
    private val encodeMouse: (
        action: MouseAction,
        button: MouseButton,
        xPx: Float,
        yPx: Float,
        geometry: MouseGeometry,
    ) -> ByteArray?,
) : CanvasScrollTarget {

    /** Leftover pixels that have not yet added up to a row. */
    private var acumuladoPx = 0f

    override fun onScrollStart() {
        acumuladoPx = 0f
    }

    override fun onScrollEnd() {
        acumuladoPx = 0f
    }

    override fun onScroll(deltaPx: Float, position: Offset): Boolean {
        val geo = geometria() ?: return false
        val alturaCelula = geo.cellHeightPx
        if (alturaCelula <= 0) return false

        acumuladoPx += deltaPx
        val linhasInteiras = (acumuladoPx / alturaCelula).toInt()
        if (linhasInteiras == 0) return true
        acumuladoPx -= linhasInteiras * alturaCelula

        // The finger moves down, the content shows the PAST. By the
        // convention used throughout the stack (and by the mouse wheel), the
        // past is negative.
        val linhas = -linhasInteiras

        return when (val acao = decidirRolagem(modos(), linhas)) {
            is AcaoDeRolagem.Viewport -> {
                rolarViewport(acao.linhas)
                podeRolarViewport(acao.linhas)
            }

            is AcaoDeRolagem.Roda -> {
                enviarRoda(acao.linhas, position, geo)
                // The wheel belongs to the remote program: there is no end
                // of scrollback of ours to reach, so the fling is never
                // interrupted here.
                true
            }

            is AcaoDeRolagem.Setas -> {
                enviarBytes(bytesDeSeta(acao.linhas, modos().cursorKeysApplication))
                true
            }

            AcaoDeRolagem.Nada -> false
        }
    }

    /**
     * One wheel event per row. It is not waste: it is literally what a desk
     * mouse produces, and it is how `htop` and `vim` count how far to scroll.
     */
    private fun enviarRoda(linhas: Int, position: Offset, geo: MouseGeometry) {
        val botao = if (linhas < 0) MouseButton.RODA_CIMA else MouseButton.RODA_BAIXO
        repeat(abs(linhas).coerceAtMost(MAXIMO_RODA_POR_EVENTO)) {
            // The wheel is a PRESS with no RELEASE — the xterm convention
            // from the very beginning. Sending a RELEASE along makes some
            // programs count two scrolls.
            encodeMouse(MouseAction.PRESS, botao, position.x, position.y, geo)
                ?.let(enviarBytes)
        }
    }

    private companion object {
        /**
         * A cap per event. An absurdly fast drag must not turn into hundreds
         * of wheel events at once on the PTY.
         */
        const val MAXIMO_RODA_POR_EVENTO = 10
    }
}
