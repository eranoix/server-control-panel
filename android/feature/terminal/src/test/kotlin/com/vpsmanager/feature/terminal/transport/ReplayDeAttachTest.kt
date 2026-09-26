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
 * server re-emits. See the comment on [AttachReplay] for the table.
 */
class AttachReplayTest {

    private fun cuu(lines: String): ByteArray = "[${lines}A".toByteArray()

    private fun stream(times: Int, lines: String): ByteArray {
        val output = ArrayList<Byte>()
        repeat(times) {
            output.addAll("texto de uma linha qualquer\r\n".toByteArray().toList())
            output.addAll(cuu(lines).toList())
        }
        return output.toByteArray()
    }

    @Test
    fun `saida append-only de shell nao e repintura`() {
        val shell = "$ ls -l\r\ntotal 4\r\ndrwxr-xr-x 2 root root 4096 dir\r\n$ ".toByteArray()
        assertEquals(0, AttachReplay.countBlockCuu(shell))
        assertFalse(AttachReplay.isDiffRepaint(shell))
    }

    @Test
    fun `redesenho de prompt do readline nao conta como repintura`() {
        // `ESC[1A` and `ESC[A` are what readline emits to redraw a two-line
        // prompt. Measured: the worst real shell on this machine had 34 of
        // them and ZERO of two lines or more.
        val readline = ByteArray(0) +
            "[1A".toByteArray().let { one -> ByteArray(0) + List(40) { one.toList() }.flatten().toByteArray() } +
            "[A".toByteArray().let { one -> ByteArray(0) + List(40) { one.toList() }.flatten().toByteArray() }
        assertEquals(0, AttachReplay.countBlockCuu(readline))
        assertFalse(AttachReplay.isDiffRepaint(readline))
    }

    @Test
    fun `parametro vazio ou zero vale uma linha, nao duas`() {
        // ECMA-48: an omitted or 0 parameter takes the command's default,
        // which for CUU is 1. Counting those as "moved up several" would
        // classify a shell as a TUI.
        assertEquals(0, AttachReplay.countBlockCuu("[A[0A[A".toByteArray()))
        assertEquals(1, AttachReplay.countBlockCuu("[2A".toByteArray()))
    }

    @Test
    fun `renderizador diferencial e reconhecido`() {
        // The real log of the "Aplicativo" session had 1354 CUU of 3+ lines
        // in the 128 KiB re-emitted; the quietest TUI had 33. 25 already
        // clears the limit of 20 comfortably and stays below the real worst
        // case.
        val ink = stream(times = 25, lines = "7")
        assertTrue(AttachReplay.countBlockCuu(ink) >= AttachReplay.REPAINT_THRESHOLD)
        assertTrue(AttachReplay.isDiffRepaint(ink))
    }

    @Test
    fun `o vao medido entre shell e TUI e respeitado dos dois lados`() {
        // 11 = the worst real shell measured. 33 = the quietest real TUI measured.
        assertFalse(AttachReplay.isDiffRepaint(stream(times = 11, lines = "4")))
        assertTrue(AttachReplay.isDiffRepaint(stream(times = 33, lines = "4")))
    }

    @Test
    fun `sequencia truncada no fim do bloco nao estoura`() {
        // The server cuts the replay at 128 KiB and only aligns on the next
        // line break — a sequence can end up truncated. Scanning that must not
        // read past the end of the array.
        assertEquals(0, AttachReplay.countBlockCuu("texto[12".toByteArray()))
        assertEquals(0, AttachReplay.countBlockCuu("texto".toByteArray()))
        assertEquals(0, AttachReplay.countBlockCuu(ByteArray(0)))
    }

    @Test
    fun `parametro absurdamente longo satura em vez de estourar`() {
        val absurd = ("[" + "9".repeat(400) + "A").toByteArray()
        assertEquals(1, AttachReplay.countBlockCuu(absurd))
    }

    @Test
    fun `outras sequencias CSI que terminam em letra diferente nao contam`() {
        // `ESC[2J` (clear screen), `ESC[10B` (move down), `ESC[3C` (right).
        val others = "[2J[10B[3C[5D".toByteArray()
        assertEquals(0, AttachReplay.countBlockCuu(others))
    }
}
