package com.vpsmanager.feature.terminal.scroll

import androidx.compose.ui.geometry.Offset
import com.vpsmanager.terminalengine.MouseAction
import com.vpsmanager.terminalengine.MouseButton
import com.vpsmanager.terminalengine.MouseGeometry
import com.vpsmanager.terminalengine.TerminalModes
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The translation from PIXEL to LINE, and the destination of each line. Runs
 * on the JVM: the rule does not depend on a device, and it is precisely the
 * one that would make scrolling look truncated or run away from the finger if
 * it were wrong.
 */
class ScrollbackGestureControllerTest {

    private val alturaCelula = 20

    private class Registro {
        var modos = TerminalModes.NENHUM
        val rolagens = mutableListOf<Int>()
        val bytes = mutableListOf<ByteArray>()
        val rodas = mutableListOf<MouseButton>()
        var haParaOnde = true
    }

    private fun controlador(r: Registro): ScrollbackGestureController =
        ScrollbackGestureController(
            modos = { r.modos },
            geometria = {
                MouseGeometry(
                    cellWidthPx = 10,
                    cellHeightPx = alturaCelula,
                    screenWidthPx = 400,
                    screenHeightPx = 200,
                )
            },
            rolarViewport = { r.rolagens += it },
            podeRolarViewport = { r.haParaOnde },
            enviarBytes = { r.bytes += it },
            encodeMouse = { _, button, _, _, _ ->
                r.rodas += button
                byteArrayOf(button.ordinal.toByte())
            },
        )

    private fun arrastar(c: ScrollbackGestureController, px: Float): Boolean {
        return c.onScroll(px, Offset(5f, 5f))
    }

    // ---- pixels become lines --------------------------------------------

    /**
     * Less than one cell scrolls nothing — but the pixel is not lost, and that
     * is what makes the content follow the finger instead of jumping three
     * lines at a time.
     */
    @Test
    fun arrasteMenorQueUmaCelula_naoRolaMasAcumula() {
        val r = Registro()
        val c = controlador(r)
        c.onScrollStart()

        arrastar(c, 8f)
        assertTrue("nada devia ter rolado ainda", r.rolagens.isEmpty())
        arrastar(c, 8f)
        assertTrue(r.rolagens.isEmpty())
        // 8+8+8 = 24 px, past one cell of 20.
        arrastar(c, 8f)
        assertEquals(listOf(-1), r.rolagens)
    }

    /** Finger DOWN shows the PAST — negative, the same convention as the wheel. */
    @Test
    fun dedoParaBaixo_rolaParaOPassado() {
        val r = Registro()
        val c = controlador(r)
        c.onScrollStart()
        arrastar(c, 60f)
        assertEquals(listOf(-3), r.rolagens)
    }

    @Test
    fun dedoParaCima_rolaParaOPresente() {
        val r = Registro()
        val c = controlador(r)
        c.onScrollStart()
        arrastar(c, -40f)
        assertEquals(listOf(2), r.rolagens)
    }

    /** Starting a new gesture clears the remainder: the previous drag does not leak. */
    @Test
    fun novoGesto_zeraOAcumulado() {
        val r = Registro()
        val c = controlador(r)
        c.onScrollStart()
        arrastar(c, 19f)
        c.onScrollEnd()

        c.onScrollStart()
        arrastar(c, 19f)
        assertTrue("a sobra do gesto anterior vazou", r.rolagens.isEmpty())
    }

    // ---- where the lines go ---------------------------------------------

    @Test
    fun semMouse_naTelaNormal_rolaOViewportLocal() {
        val r = Registro()
        val c = controlador(r)
        c.onScrollStart()
        arrastar(c, 40f)
        assertEquals(listOf(-2), r.rolagens)
        assertTrue("não devia mandar byte nenhum ao PTY", r.bytes.isEmpty())
    }

    /** With `htop` open, the drag becomes the wheel — and nothing scrolls locally. */
    @Test
    fun comMouseAtivo_arrasteViraRoda_eNaoRolaLocalmente() {
        val r = Registro()
        r.modos = TerminalModes.NENHUM.copy(mouseTracking = true)
        val c = controlador(r)
        c.onScrollStart()
        arrastar(c, 60f)

        assertTrue("o viewport local não podia se mover", r.rolagens.isEmpty())
        assertEquals(
            "uma roda por linha, para cima",
            listOf(MouseButton.RODA_CIMA, MouseButton.RODA_CIMA, MouseButton.RODA_CIMA),
            r.rodas,
        )
        assertEquals(3, r.bytes.size)
    }

    @Test
    fun comMouseAtivo_dedoParaCima_mandaRodaParaBaixo() {
        val r = Registro()
        r.modos = TerminalModes.NENHUM.copy(mouseTracking = true)
        val c = controlador(r)
        c.onScrollStart()
        arrastar(c, -40f)
        assertEquals(listOf(MouseButton.RODA_BAIXO, MouseButton.RODA_BAIXO), r.rodas)
    }

    /** `less`: alternate screen, no mouse, 1007 on — the drag becomes arrow keys. */
    @Test
    fun telaAlternativaComAltScroll_mandaSetas() {
        val r = Registro()
        r.modos = TerminalModes.NENHUM.copy(altScreen = true, altScroll = true)
        val c = controlador(r)
        c.onScrollStart()
        arrastar(c, 40f)

        assertTrue(r.rolagens.isEmpty())
        assertEquals(1, r.bytes.size)
        assertArrayEquals("\u001b[A\u001b[A".toByteArray(), r.bytes.single())
    }

    @Test
    fun telaAlternativaComDECCKM_mandaSetasEmModoAplicacao() {
        val r = Registro()
        r.modos = TerminalModes.NENHUM.copy(
            altScreen = true,
            altScroll = true,
            cursorKeysApplication = true,
        )
        val c = controlador(r)
        c.onScrollStart()
        arrastar(c, 20f)
        assertArrayEquals("\u001bOA".toByteArray(), r.bytes.single())
    }

    /** Full screen without 1007: nothing happens, and the gesture says it is over. */
    @Test
    fun telaAlternativaSemAltScroll_naoFazNadaEEncerraAInercia() {
        val r = Registro()
        r.modos = TerminalModes.NENHUM.copy(altScreen = true, altScroll = false)
        val c = controlador(r)
        c.onScrollStart()

        assertFalse("a inércia tinha que parar", arrastar(c, 40f))
        assertTrue(r.rolagens.isEmpty())
        assertTrue(r.bytes.isEmpty())
    }

    // ---- end of the history ----------------------------------------------

    /**
     * It reached the top: the gesture answers `false` and the fling stops,
     * instead of grinding against the wall until the deceleration curve runs
     * out on its own.
     */
    @Test
    fun noFimDoHistorico_avisaQueNaoHaParaOnde() {
        val r = Registro()
        r.haParaOnde = false
        val c = controlador(r)
        c.onScrollStart()
        assertFalse(arrastar(c, 40f))
    }

    /** The wheel belongs to the remote program: there is no end of OUR history there. */
    @Test
    fun comMouseAtivo_aInerciaNuncaEInterrompidaPeloFimLocal() {
        val r = Registro()
        r.haParaOnde = false
        r.modos = TerminalModes.NENHUM.copy(mouseTracking = true)
        val c = controlador(r)
        c.onScrollStart()
        assertTrue(arrastar(c, 40f))
    }

    /** An absurd drag must not turn into hundreds of wheel events on the PTY. */
    @Test
    fun arrasteAbsurdo_temTetoDeRodasPorEvento() {
        val r = Registro()
        r.modos = TerminalModes.NENHUM.copy(mouseTracking = true)
        val c = controlador(r)
        c.onScrollStart()
        arrastar(c, 20f * 500)
        assertEquals(10, r.rodas.size)
    }

    @Test
    fun semGeometria_naoFazNada() {
        val c = ScrollbackGestureController(
            modos = { TerminalModes.NENHUM },
            geometria = { null },
            rolarViewport = { throw AssertionError("não podia rolar sem geometria") },
            podeRolarViewport = { true },
            enviarBytes = { throw AssertionError("não podia mandar byte sem geometria") },
            encodeMouse = { _, _, _, _, _ -> null },
        )
        c.onScrollStart()
        assertFalse(c.onScroll(100f, Offset.Zero))
    }
}
