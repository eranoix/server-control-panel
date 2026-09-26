package com.vpsmanager.terminalengine

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.runner.RunWith
import org.junit.Test
import androidx.test.ext.junit.runners.AndroidJUnit4

/**
 * The defect the app's owner reported — *"the mouse function is producing
 * crazy text in the terminal"* — and its cause, pinned by tests against the
 * real libghostty-vt.
 *
 * The root cause was the app emitting mouse sequences because a MANUAL SWITCH
 * was on. But whoever decides whether a mouse event has a recipient is the
 * remote PROGRAM, by enabling tracking through DECSET (1000 click / 1002 drag
 * / 1003 any movement), and a `bash` prompt never enables it. With no
 * recipient, the bytes reach the shell as TEXT and appear typed on the command
 * line.
 *
 * These tests run on the device because the only proof that counts is against
 * the real VT emulator: it is the one that knows which mode the program asked
 * for. A JVM test with a hand-written table would prove only that the table
 * agrees with itself — which is exactly what existed before, and it passed
 * green with the defect live.
 */
@RunWith(AndroidJUnit4::class)
class MousePasteEncodingTest {

    /** 20x40 px per cell, an 80x24 grid — round numbers so the cell arithmetic is obvious. */
    private val geometry = MouseGeometry(
        cellWidthPx = 20,
        cellHeightPx = 40,
        screenWidthPx = 80 * 20,
        screenHeightPx = 24 * 40,
    )

    private fun engine(): TerminalEngine = TerminalEngine.create(cols = 80, rows = 24)

    private fun TerminalEngine.escreva(vt: String) = write(vt.toByteArray(Charsets.US_ASCII))

    /** Centre of cell (col, row) — never the edge, so rounding is not what the test is about. */
    private fun centro(col: Int, row: Int): Pair<Float, Float> =
        (col * 20f + 10f) to (row * 40f + 20f)

    // ---- O defeito -------------------------------------------------------

    @Test
    fun semRastreamentoAtivo_toqueNaoEmiteByteNenhum() {
        val engine = engine()
        try {
            // A freshly created terminal is the `bash` prompt: nobody asked for mouse.
            assertFalse("bash num prompt comum não pede mouse", engine.modes().mouseTracking)

            val (x, y) = centro(col = 27, row = 14)
            val press = engine.encodeMouse(MouseAction.PRESS, MouseButton.ESQUERDO, x, y, geometry, anyButtonPressed = true)
            val release = engine.encodeMouse(MouseAction.RELEASE, MouseButton.ESQUERDO, x, y, geometry)

            assertNull("sem rastreamento, o toque não pode produzir byte nenhum", press)
            assertNull("nem o soltar", release)
        } finally {
            engine.close()
        }
    }

    @Test
    fun comRastreamentoSgrAtivo_emiteAsequenciaSgrCorreta() {
        val engine = engine()
        try {
            // What an `htop`/`vim` writes when it turns the mouse on: click
            // tracking (1000) with SGR encoding (1006).
            engine.escreva("\u001b[?1000h\u001b[?1006h")
            assertTrue("o programa ativou rastreamento", engine.modes().mouseTracking)

            val (x, y) = centro(col = 4, row = 9)
            val press = engine.encodeMouse(MouseAction.PRESS, MouseButton.ESQUERDO, x, y, geometry, anyButtonPressed = true)
            val release = engine.encodeMouse(MouseAction.RELEASE, MouseButton.ESQUERDO, x, y, geometry)

            // SGR: CSI < Cb ; Cx ; Cy M (pressiona) / m (solta), coordenadas 1-based.
            assertArrayEquals("\u001b[<0;5;10M".toByteArray(Charsets.US_ASCII), press)
            assertArrayEquals("\u001b[<0;5;10m".toByteArray(Charsets.US_ASCII), release)
        } finally {
            engine.close()
        }
    }

    @Test
    fun programaDesligaRastreamento_voltaANaoEmitirNada() {
        val engine = engine()
        try {
            engine.escreva("\u001b[?1000h\u001b[?1006h")
            val (x, y) = centro(col = 1, row = 1)
            assertTrue(engine.encodeMouse(MouseAction.PRESS, MouseButton.ESQUERDO, x, y, geometry, anyButtonPressed = true) != null)

            // Leaving `htop` restores the mode. Nobody tells the app — it has
            // to RE-READ the state, which is the whole point.
            engine.escreva("\u001b[?1000l")

            assertFalse(engine.modes().mouseTracking)
            assertNull(
                "desligado o rastreamento, o toque volta a não ter destinatário",
                engine.encodeMouse(MouseAction.PRESS, MouseButton.ESQUERDO, x, y, geometry, anyButtonPressed = true),
            )
        } finally {
            engine.close()
        }
    }

    @Test
    fun formatoX10_naoEmiteSgr() {
        val engine = engine()
        try {
            // Tracking WITHOUT 1006: the program wants the old X10 format. The
            // old app sent SGR regardless — a sequence this program does not
            // understand.
            engine.escreva("\u001b[?1000h")

            val (x, y) = centro(col = 4, row = 9)
            val press = engine.encodeMouse(MouseAction.PRESS, MouseButton.ESQUERDO, x, y, geometry, anyButtonPressed = true)

            // X10: CSI M Cb Cx Cy, with 32 added to each byte and 1-based coordinates.
            val esperado = byteArrayOf(
                0x1b, '['.code.toByte(), 'M'.code.toByte(),
                (32 + 0).toByte(), (32 + 5).toByte(), (32 + 10).toByte(),
            )
            assertArrayEquals(esperado, press)
        } finally {
            engine.close()
        }
    }

    @Test
    fun movimentoDuranteOarraste_relataAcelulaSobODedo() {
        val engine = engine()
        try {
            // 1002 = drag tracking, which is what a `vim` turns on to follow
            // the pointer with the button held down.
            engine.escreva("\u001b[?1002h\u001b[?1006h")
            val (x, y) = centro(col = 3, row = 3)

            engine.encodeMouse(MouseAction.PRESS, MouseButton.ESQUERDO, x, y, geometry, anyButtonPressed = true)
            val mesmaCelula = engine.encodeMouse(
                MouseAction.MOTION, MouseButton.ESQUERDO, x + 2f, y + 2f, geometry, anyButtonPressed = true,
            )
            val outraCelula = engine.encodeMouse(
                MouseAction.MOTION, MouseButton.ESQUERDO, x + 20f, y, geometry, anyButtonPressed = true,
            )

            // 1-based coordinates; bit 5 (adding 32 to the button code) marks
            // "movement with the button held down".
            assertArrayEquals("\u001b[<32;4;4M".toByteArray(Charsets.US_ASCII), mesmaCelula)
            assertArrayEquals("\u001b[<32;5;4M".toByteArray(Charsets.US_ASCII), outraCelula)
        } finally {
            engine.close()
        }
    }

    @Test
    fun oCodificadorNaoDeduplicaMovimentoNoModo1002() {
        // Finding measured HERE, against the real library: even with
        // `TRACK_LAST_CELL` on, two movements in the SAME cell produce two
        // identical reports in button tracking mode — which is exactly the
        // mode a finger drag uses.
        //
        // This test exists so the finding is not lost: it is what justifies
        // the deduplication living in `MouseReportGestureController`, on the
        // Kotlin side. If a future libghostty-vt starts deduplicating, this
        // test fails and says the layer above can go.
        val engine = engine()
        try {
            engine.escreva("\u001b[?1002h\u001b[?1006h")
            val (x, y) = centro(col = 3, row = 3)
            engine.encodeMouse(MouseAction.PRESS, MouseButton.ESQUERDO, x, y, geometry, anyButtonPressed = true)

            val primeiro = engine.encodeMouse(
                MouseAction.MOTION, MouseButton.ESQUERDO, x + 2f, y + 2f, geometry, anyButtonPressed = true,
            )
            val segundo = engine.encodeMouse(
                MouseAction.MOTION, MouseButton.ESQUERDO, x + 4f, y + 4f, geometry, anyButtonPressed = true,
            )

            assertArrayEquals(
                "sem deduplicação na biblioteca: o segundo movimento repete o primeiro",
                primeiro,
                segundo,
            )
        } finally {
            engine.close()
        }
    }

    // ---- Colagem entre colchetes ----------------------------------------

    @Test
    fun colagemSemDecset2004_naoLevaMarcadoresEQuebraViraRetorno() {
        val engine = engine()
        try {
            assertFalse(engine.modes().bracketedPaste)

            val bytes = engine.encodePaste("um\ndois")

            val texto = String(bytes, Charsets.UTF_8)
            assertFalse("sem 2004, os marcadores seriam texto literal na linha", texto.contains("\u001b[200~"))
            assertFalse(texto.contains("\u001b[201~"))
            assertEquals("um\rdois", texto)
        } finally {
            engine.close()
        }
    }

    @Test
    fun colagemComDecset2004_vaiEnvoltaPelosMarcadores() {
        val engine = engine()
        try {
            engine.escreva("\u001b[?2004h")
            assertTrue("o programa ligou colagem entre colchetes", engine.modes().bracketedPaste)

            val texto = String(engine.encodePaste("um\ndois"), Charsets.UTF_8)

            assertEquals("\u001b[200~um\ndois\u001b[201~", texto)
        } finally {
            engine.close()
        }
    }

    @Test
    fun colagemComMarcadorDeFimEmbutido_naoDeixaOtextoVirarComando() {
        val engine = engine()
        try {
            engine.escreva("\u001b[?2004h")

            // The classic paste attack: an ESC[201~ inside the text would
            // close the paste halfway through and the rest would be executed
            // as a COMMAND by the shell.
            val texto = String(engine.encodePaste("inofensivo\u001b[201~rm -rf /"), Charsets.UTF_8)

            assertTrue("o embrulho começa e termina onde deve", texto.startsWith("\u001b[200~"))
            assertTrue(texto.endsWith("\u001b[201~"))
            val miolo = texto.removePrefix("\u001b[200~").removeSuffix("\u001b[201~")
            assertFalse("nenhum marcador de fim pode sobreviver no miolo", miolo.contains("\u001b[201~"))
            assertFalse("nenhum ESC cru pode sobreviver no miolo", miolo.contains("\u001b"))
        } finally {
            engine.close()
        }
    }

    @Test
    fun modos_saoIndependentesEntreSi() {
        val engine = engine()
        try {
            assertEquals(TerminalModes.NENHUM, engine.modes())

            engine.escreva("\u001b[?2004h")
            assertEquals(TerminalModes(mouseTracking = false, bracketedPaste = true), engine.modes())

            engine.escreva("\u001b[?1003h")
            assertEquals(TerminalModes(mouseTracking = true, bracketedPaste = true), engine.modes())

            engine.escreva("\u001b[?2004l")
            assertEquals(TerminalModes(mouseTracking = true, bracketedPaste = false), engine.modes())
        } finally {
            engine.close()
        }
    }
}
