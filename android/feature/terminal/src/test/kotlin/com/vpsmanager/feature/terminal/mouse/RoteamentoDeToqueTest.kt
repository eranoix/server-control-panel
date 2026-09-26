package com.vpsmanager.feature.terminal.mouse

import androidx.compose.ui.geometry.Offset
import com.vpsmanager.feature.terminal.input.ByteSink
import com.vpsmanager.feature.terminal.selection.DragPhase
import com.vpsmanager.terminalengine.MouseAction
import com.vpsmanager.terminalengine.MouseButton
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

private class SinkGravador : ByteSink {
    val enviados = mutableListOf<ByteArray>()
    override fun send(bytes: ByteArray) {
        enviados += bytes
    }
}

/**
 * A fake encoder that behaves like the real one: it returns `null` for as long
 * as the "remote program" has not asked for mouse tracking.
 *
 * That is the rule the app was violating. The real encoder
 * (`TerminalEngine.encodeMouse`) returns zero bytes when no tracking is
 * active, and the proof against the real libghostty-vt lives in
 * `:terminal-engine`'s `MousePasteEncodingTest` — what is pinned down here is
 * the CONTROLLER's behaviour in the face of those two answers.
 */
private class EncoderFalso(var pedeMouse: Boolean) : MouseEventEncoder {
    val acoes = mutableListOf<MouseAction>()

    /** A 20x40 px cell, as in the rest of this module's gesture tests. */
    override fun encode(
        action: MouseAction,
        position: Offset,
        button: MouseButton,
        anyButtonPressed: Boolean,
    ): ByteArray? {
        acoes += action
        if (!pedeMouse) return null
        val terminador = if (action == MouseAction.RELEASE) 'm' else 'M'
        val cb = if (action == MouseAction.MOTION) 32 else 0
        val coluna = (position.x / 20).toInt() + 1
        val linha = (position.y / 40).toInt() + 1
        return "\u001b[<$cb;$coluna;$linha$terminador".toByteArray(Charsets.US_ASCII)
    }
}

/**
 * The defect the app's owner reported: *"the mouse feature is producing crazy
 * text in the terminal"*.
 *
 * The cause was a MANUAL switch: set to "Mouse", the app emitted mouse
 * sequences into the stream whether or not anything was there to interpret
 * them. Since a terminal application has to ASK for tracking (DECSET
 * 1000/1002/1003) and a `bash` prompt never does, the bytes reached the shell
 * as TEXT and appeared typed on the command line.
 *
 * These tests pin down both sides: nothing goes out when nobody asked, and
 * what does go out when they did is what the program expects.
 */
class RoteamentoDeToqueTest {

    // ---- Routing: the state in charge is the terminal's, not the app's ----

    @Test
    fun semProgramaPedindoMouse_oGestoEsempreDoApp() {
        val roteamento = RoteamentoDeToque { false }

        assertFalse("não há mouse pra relatar", roteamento.programaPedeMouse())
        assertTrue("o gesto é do app: seleção e teclado", roteamento.toqueEDoApp())
    }

    @Test
    fun comProgramaPedindoMouse_oGestoEdoPrograma() {
        val roteamento = RoteamentoDeToque { true }

        assertTrue(roteamento.programaPedeMouse())
        assertFalse("dentro de um htop o toque é clique, não seleção", roteamento.toqueEDoApp())
    }

    @Test
    fun naoHaMaisPreferenciaQuePossaContradizerOprogramaRemoto() {
        // This is the assertion that pins down the REMOVAL the app's owner
        // asked for.
        //
        // There used to be a two-position `MouseTouchPreference` here, heir to
        // the manual mouse switch. It forced people to understand DECSET
        // 1000/1002/1003 in order to decide a rare case, and it is gone. What
        // must not come back is the possibility of the app CONTRADICTING the
        // remote program: if the program asked for the mouse, the touch is
        // its; if it did not, the touch is the app's. No third option, no
        // stored state, nothing to toggle.
        //
        // The way to select inside a full-screen program still exists and does
        // not come through here: it is the LONG PRESS, which anchors the
        // selection ahead of any routing.
        val pedindo = RoteamentoDeToque { true }
        val naoPedindo = RoteamentoDeToque { false }

        assertEquals(
            "o dono do gesto é função APENAS do estado do emulador",
            listOf(false, true),
            listOf(pedindo.toqueEDoApp(), naoPedindo.toqueEDoApp()),
        )
    }

    @Test
    fun oEstadoEreLidoAcadaPergunta_naoLembrado() {
        // An `htop` opening turns tracking on; one closing turns it off.
        // Nobody tells the app — it has to re-read.
        var pedindo = false
        val roteamento = RoteamentoDeToque { pedindo }

        assertTrue(roteamento.toqueEDoApp())
        pedindo = true
        assertFalse("abriu o htop: o toque passa a ser dele", roteamento.toqueEDoApp())
        pedindo = false
        assertTrue("saiu do htop: o toque volta a ser do app", roteamento.toqueEDoApp())
    }

    // ---- The controller: nothing goes out when the encoder says "nothing" ----

    @Test
    fun semRastreamentoAtivo_nenhumByteEenviado() {
        val encoder = EncoderFalso(pedeMouse = false)
        val sink = SinkGravador()
        val controller = MouseReportGestureController(encoder, sink)

        controller.onTap(Offset(15f, 25f), taps = 1)
        controller.onDrag(Offset(15f, 25f), DragPhase.START)
        controller.onDrag(Offset(35f, 45f), DragPhase.MOVE)
        controller.onDrag(Offset(35f, 45f), DragPhase.END)

        assertTrue(
            "sem rastreamento ativo nenhum byte pode chegar ao PTY — era exatamente esse lixo que aparecia na linha de comando",
            sink.enviados.isEmpty(),
        )
        assertFalse("mas o codificador foi consultado: quem decide é ele", encoder.acoes.isEmpty())
    }

    @Test
    fun comRastreamentoAtivo_umToqueViraOparPressionaSolta() {
        val encoder = EncoderFalso(pedeMouse = true)
        val sink = SinkGravador()
        val controller = MouseReportGestureController(encoder, sink)

        controller.onTap(Offset(15f, 25f), taps = 1)

        assertEquals("um clique é pressiona + solta", 2, sink.enviados.size)
        assertEquals(listOf(MouseAction.PRESS, MouseAction.RELEASE), encoder.acoes)
        assertArrayEquals("\u001b[<0;1;1M".toByteArray(Charsets.US_ASCII), sink.enviados[0])
        assertArrayEquals("\u001b[<0;1;1m".toByteArray(Charsets.US_ASCII), sink.enviados[1])
    }

    @Test
    fun comRastreamentoAtivo_oArrasteMandaPressionaMoveSolta() {
        val encoder = EncoderFalso(pedeMouse = true)
        val sink = SinkGravador()
        val controller = MouseReportGestureController(encoder, sink)

        controller.onDrag(Offset(1f, 1f), DragPhase.START)
        controller.onDrag(Offset(21f, 1f), DragPhase.MOVE)
        controller.onDrag(Offset(21f, 1f), DragPhase.END)

        assertEquals(
            listOf(MouseAction.PRESS, MouseAction.MOTION, MouseAction.RELEASE),
            encoder.acoes,
        )
        assertEquals(3, sink.enviados.size)
    }

    @Test
    fun toqueDuploEmModoMouse_saoDoisCliques_naoSelecaoDePalavra() {
        // Double-tap selection is the APP's gesture. When the program owns
        // the touch, two quick taps are two clicks — which is what an `htop`
        // or a text-mode file manager expects.
        val encoder = EncoderFalso(pedeMouse = true)
        val sink = SinkGravador()
        val controller = MouseReportGestureController(encoder, sink)

        controller.onTap(Offset(15f, 25f), taps = 1)
        controller.onTap(Offset(15f, 25f), taps = 2)

        assertEquals(4, sink.enviados.size)
        assertEquals(
            listOf(MouseAction.PRESS, MouseAction.RELEASE, MouseAction.PRESS, MouseAction.RELEASE),
            encoder.acoes,
        )
    }

    @Test
    fun codificadorQueDevolveVetorVazio_naoGeraQuadro() {
        // Movement within the same cell: the native encoder returns zero
        // bytes. Sending an empty frame to the PTY is not harmless — it is
        // network noise per pixel of drag.
        val sink = SinkGravador()
        val controller = MouseReportGestureController(
            { _, _, _, _ -> ByteArray(0) },
            sink,
        )

        controller.onDrag(Offset(1f, 1f), DragPhase.MOVE)

        assertTrue(sink.enviados.isEmpty())
    }

    @Test
    fun movimentoDentroDaMesmaCelula_naoRepeteRelatorio() {
        // A dragging finger produces one touch event per frame — dozens of
        // them within the same cell. Without deduplication each would become a
        // WebSocket frame and a PTY write, and the repeated report says
        // nothing new: the program already knows where the pointer is.
        //
        // libghostty-vt does NOT do that suppression in mode 1002 even with
        // `TRACK_LAST_CELL` on — measured on the emulator, see
        // `MousePasteEncodingTest.oCodificadorNaoDeduplicaMovimentoNoModo1002`.
        val encoder = EncoderFalso(pedeMouse = true)
        val sink = SinkGravador()
        val controller = MouseReportGestureController(encoder, sink)

        controller.onDrag(Offset(10f, 20f), DragPhase.START)
        controller.onDrag(Offset(12f, 22f), DragPhase.MOVE)
        controller.onDrag(Offset(14f, 24f), DragPhase.MOVE)
        controller.onDrag(Offset(18f, 26f), DragPhase.MOVE)

        assertEquals(
            "pressiona + UM movimento, por mais que o dedo ande dentro da célula",
            2,
            sink.enviados.size,
        )
    }

    @Test
    fun aoMudarDeCelula_oMovimentoVoltaAserRelatado() {
        val encoder = EncoderFalso(pedeMouse = true)
        val sink = SinkGravador()
        val controller = MouseReportGestureController(encoder, sink)

        controller.onDrag(Offset(10f, 20f), DragPhase.START)
        controller.onDrag(Offset(12f, 22f), DragPhase.MOVE)
        controller.onDrag(Offset(32f, 22f), DragPhase.MOVE)

        assertEquals(3, sink.enviados.size)
        assertArrayEquals("\u001b[<32;2;1M".toByteArray(Charsets.US_ASCII), sink.enviados[2])
    }

    @Test
    fun umNovoArraste_naoHerdaAmemoriaDoAnterior() {
        // Without this reset, dragging twice in a row to the same cell would
        // make the second drag lose its first movement.
        val encoder = EncoderFalso(pedeMouse = true)
        val sink = SinkGravador()
        val controller = MouseReportGestureController(encoder, sink)

        controller.onDrag(Offset(10f, 20f), DragPhase.START)
        controller.onDrag(Offset(12f, 22f), DragPhase.MOVE)
        controller.onDrag(Offset(12f, 22f), DragPhase.END)
        sink.enviados.clear()

        controller.onDrag(Offset(10f, 20f), DragPhase.START)
        controller.onDrag(Offset(12f, 22f), DragPhase.MOVE)

        assertEquals("o movimento do novo arraste tem que sair", 2, sink.enviados.size)
    }
}
