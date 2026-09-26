package com.vpsmanager.feature.terminal.scroll

import com.vpsmanager.terminalengine.TerminalModes
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * The decision table for the vertical drag. This is where it is easy to be
 * silently wrong — scrolling the local history inside an `htop` raises no
 * error, it just does not work — so every row of the table has its own test.
 */
class ScrollGesturePolicyTest {

    private val shell = TerminalModes.NENHUM

    @Test
    fun telaNormalSemMouse_rolaOHistoricoLocal() {
        assertEquals(AcaoDeRolagem.Viewport(-3), decidirRolagem(shell, -3))
        assertEquals(AcaoDeRolagem.Viewport(5), decidirRolagem(shell, 5))
    }

    /** `htop`, `vim` with mouse, `less`: the gesture belongs to the program, not to us. */
    @Test
    fun programaPediuMouse_viraRoda_mesmoNaTelaNormal() {
        val comMouse = shell.copy(mouseTracking = true)
        assertEquals(AcaoDeRolagem.Roda(-3), decidirRolagem(comMouse, -3))
    }

    @Test
    fun programaPediuMouse_viraRoda_tambemNaTelaAlternativa() {
        val htop = shell.copy(mouseTracking = true, altScreen = true, altScroll = true)
        assertEquals(AcaoDeRolagem.Roda(-2), decidirRolagem(htop, -2))
    }

    /** `less`/`man`: alternate screen, no mouse, 1007 on (the default). */
    @Test
    fun telaAlternativaComAltScroll_viraSeta() {
        val less = shell.copy(altScreen = true, altScroll = true)
        assertEquals(AcaoDeRolagem.Setas(-4), decidirRolagem(less, -4))
    }

    /**
     * Alternate screen without 1007 and without mouse: there is no history and
     * the program has declared it does not want a wheel. Doing nothing is the
     * right answer — faking movement here would be lying.
     */
    @Test
    fun telaAlternativaSemAltScroll_naoFazNada() {
        val cheia = shell.copy(altScreen = true, altScroll = false)
        assertEquals(AcaoDeRolagem.Nada, decidirRolagem(cheia, -4))
    }

    @Test
    fun arrasteDeZeroLinhas_naoFazNada() {
        assertEquals(AcaoDeRolagem.Nada, decidirRolagem(shell, 0))
        assertEquals(AcaoDeRolagem.Nada, decidirRolagem(shell.copy(mouseTracking = true), 0))
    }

    // ---- arrow bytes -----------------------------------------------------

    @Test
    fun setaNormal_usaCSI() {
        assertArrayEquals("\u001b[A".toByteArray(), bytesDeSeta(-1, cursorKeysApplication = false))
        assertArrayEquals("\u001b[B".toByteArray(), bytesDeSeta(1, cursorKeysApplication = false))
    }

    /** DECCKM on: SS3, not CSI. The wrong form does not scroll — it becomes garbage. */
    @Test
    fun setaEmModoAplicacao_usaSS3() {
        assertArrayEquals("\u001bOA".toByteArray(), bytesDeSeta(-1, cursorKeysApplication = true))
        assertArrayEquals("\u001bOB".toByteArray(), bytesDeSeta(1, cursorKeysApplication = true))
    }

    @Test
    fun setaRepeteUmaVezPorLinha() {
        assertArrayEquals(
            "\u001b[A\u001b[A\u001b[A".toByteArray(),
            bytesDeSeta(-3, cursorKeysApplication = false),
        )
    }

    @Test
    fun setaDeZeroLinhas_naoProduzByte() {
        assertEquals(0, bytesDeSeta(0, cursorKeysApplication = false).size)
    }
}
