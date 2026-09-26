package com.vpsmanager.feature.terminal.transport

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Calibration of the non-reproducible replay detector.
 *
 * The numbers in these tests are not invented: they come from counting across
 * the 30 real session logs on this machine, over the same 128 KiB window the
 * server re-emits. See the comment on [ReplayDeAttach] for the table.
 */
class ReplayDeAttachTest {

    private fun cuu(linhas: String): ByteArray = "[${linhas}A".toByteArray()

    private fun fluxo(vezes: Int, linhas: String): ByteArray {
        val saida = ArrayList<Byte>()
        repeat(vezes) {
            saida.addAll("texto de uma linha qualquer\r\n".toByteArray().toList())
            saida.addAll(cuu(linhas).toList())
        }
        return saida.toByteArray()
    }

    @Test
    fun `saida append-only de shell nao e repintura`() {
        val shell = "$ ls -l\r\ntotal 4\r\ndrwxr-xr-x 2 root root 4096 dir\r\n$ ".toByteArray()
        assertEquals(0, ReplayDeAttach.contarCuuDeBloco(shell))
        assertFalse(ReplayDeAttach.ehRepinturaDiferencial(shell))
    }

    @Test
    fun `redesenho de prompt do readline nao conta como repintura`() {
        // `ESC[1A` and `ESC[A` are what readline emits to redraw a two-line
        // prompt. Measured: the worst real shell on this machine had 34 of
        // them and ZERO of two lines or more.
        val readline = ByteArray(0) +
            "[1A".toByteArray().let { um -> ByteArray(0) + List(40) { um.toList() }.flatten().toByteArray() } +
            "[A".toByteArray().let { um -> ByteArray(0) + List(40) { um.toList() }.flatten().toByteArray() }
        assertEquals(0, ReplayDeAttach.contarCuuDeBloco(readline))
        assertFalse(ReplayDeAttach.ehRepinturaDiferencial(readline))
    }

    @Test
    fun `parametro vazio ou zero vale uma linha, nao duas`() {
        // ECMA-48: an omitted or 0 parameter takes the command's default,
        // which for CUU is 1. Counting those as "moved up several" would
        // classify a shell as a TUI.
        assertEquals(0, ReplayDeAttach.contarCuuDeBloco("[A[0A[A".toByteArray()))
        assertEquals(1, ReplayDeAttach.contarCuuDeBloco("[2A".toByteArray()))
    }

    @Test
    fun `renderizador diferencial e reconhecido`() {
        // The real log of the "Aplicativo" session had 1354 CUU of 3+ lines
        // in the 128 KiB re-emitted; the quietest TUI had 33. 25 already
        // clears the limit of 20 comfortably and stays below the real worst
        // case.
        val ink = fluxo(vezes = 25, linhas = "7")
        assertTrue(ReplayDeAttach.contarCuuDeBloco(ink) >= ReplayDeAttach.LIMITE_REPINTURA)
        assertTrue(ReplayDeAttach.ehRepinturaDiferencial(ink))
    }

    @Test
    fun `o vao medido entre shell e TUI e respeitado dos dois lados`() {
        // 11 = the worst real shell measured. 33 = the quietest real TUI measured.
        assertFalse(ReplayDeAttach.ehRepinturaDiferencial(fluxo(vezes = 11, linhas = "4")))
        assertTrue(ReplayDeAttach.ehRepinturaDiferencial(fluxo(vezes = 33, linhas = "4")))
    }

    @Test
    fun `sequencia truncada no fim do bloco nao estoura`() {
        // The server cuts the replay at 128 KiB and only aligns on the next
        // line break — a sequence can end up truncated. Scanning that must not
        // read past the end of the array.
        assertEquals(0, ReplayDeAttach.contarCuuDeBloco("texto[12".toByteArray()))
        assertEquals(0, ReplayDeAttach.contarCuuDeBloco("texto".toByteArray()))
        assertEquals(0, ReplayDeAttach.contarCuuDeBloco(ByteArray(0)))
    }

    @Test
    fun `parametro absurdamente longo satura em vez de estourar`() {
        val absurdo = ("[" + "9".repeat(400) + "A").toByteArray()
        assertEquals(1, ReplayDeAttach.contarCuuDeBloco(absurdo))
    }

    @Test
    fun `outras sequencias CSI que terminam em letra diferente nao contam`() {
        // `ESC[2J` (clear screen), `ESC[10B` (move down), `ESC[3C` (right).
        val outras = "[2J[10B[3C[5D".toByteArray()
        assertEquals(0, ReplayDeAttach.contarCuuDeBloco(outras))
    }
}
