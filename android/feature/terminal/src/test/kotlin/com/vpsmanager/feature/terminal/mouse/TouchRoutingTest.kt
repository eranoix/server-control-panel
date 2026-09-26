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

private class RecordingSink : ByteSink {
    val sent = mutableListOf<ByteArray>()
    override fun send(bytes: ByteArray) {
        sent += bytes
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
private class FakeEncoder(var wantsMouse: Boolean) : MouseEventEncoder {
    val actions = mutableListOf<MouseAction>()

    /** A 20x40 px cell, as in the rest of this module's gesture tests. */
    override fun encode(
        action: MouseAction,
        position: Offset,
        button: MouseButton,
        anyButtonPressed: Boolean,
    ): ByteArray? {
        actions += action
        if (!wantsMouse) return null
        val terminator = if (action == MouseAction.RELEASE) 'm' else 'M'
        val cb = if (action == MouseAction.MOTION) 32 else 0
        val column = (position.x / 20).toInt() + 1
        val line = (position.y / 40).toInt() + 1
        return "\u001b[<$cb;$column;$line$terminator".toByteArray(Charsets.US_ASCII)
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
class TouchRoutingTest {

    // ---- Routing: the state in charge is the terminal's, not the app's ----

    @Test
    fun noProgramAskingForMouse_gestureAlwaysBelongsToApp() {
        val routing = TouchRouting { false }

        assertFalse("não há mouse pra relatar", routing.programWantsMouse())
        assertTrue("o gesto é do app: seleção e teclado", routing.tapBelongsToApp())
    }

    @Test
    fun programAskingForMouse_gestureBelongsToProgram() {
        val routing = TouchRouting { true }

        assertTrue(routing.programWantsMouse())
        assertFalse("dentro de um htop o toque é clique, não seleção", routing.tapBelongsToApp())
    }

    @Test
    fun noPreferenceCanOverrideRemoteProgram() {
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
        val requesting = TouchRouting { true }
        val notRequesting = TouchRouting { false }

        assertEquals(
            "o dono do gesto é função APENAS do estado do emulador",
            listOf(false, true),
            listOf(requesting.tapBelongsToApp(), notRequesting.tapBelongsToApp()),
        )
    }

    @Test
    fun stateIsReadOnEachQuery_notRemembered() {
        // An `htop` opening turns tracking on; one closing turns it off.
        // Nobody tells the app — it has to re-read.
        var requesting = false
        val routing = TouchRouting { requesting }

        assertTrue(routing.tapBelongsToApp())
        requesting = true
        assertFalse("abriu o htop: o toque passa a ser dele", routing.tapBelongsToApp())
        requesting = false
        assertTrue("saiu do htop: o toque volta a ser do app", routing.tapBelongsToApp())
    }

    // ---- The controller: nothing goes out when the encoder says "nothing" ----

    @Test
    fun trackingOff_noBytesSent() {
        val encoder = FakeEncoder(wantsMouse = false)
        val sink = RecordingSink()
        val controller = MouseReportGestureController(encoder, sink)

        controller.onTap(Offset(15f, 25f), taps = 1)
        controller.onDrag(Offset(15f, 25f), DragPhase.START)
        controller.onDrag(Offset(35f, 45f), DragPhase.MOVE)
        controller.onDrag(Offset(35f, 45f), DragPhase.END)

        assertTrue(
            "sem rastreamento ativo nenhum byte pode chegar ao PTY — era exatamente esse lixo que aparecia na linha de comando",
            sink.sent.isEmpty(),
        )
        assertFalse("mas o codificador foi consultado: quem decide é ele", encoder.actions.isEmpty())
    }

    @Test
    fun trackingOn_tapBecomesPressReleasePair() {
        val encoder = FakeEncoder(wantsMouse = true)
        val sink = RecordingSink()
        val controller = MouseReportGestureController(encoder, sink)

        controller.onTap(Offset(15f, 25f), taps = 1)

        assertEquals("um clique é pressiona + solta", 2, sink.sent.size)
        assertEquals(listOf(MouseAction.PRESS, MouseAction.RELEASE), encoder.actions)
        assertArrayEquals("\u001b[<0;1;1M".toByteArray(Charsets.US_ASCII), sink.sent[0])
        assertArrayEquals("\u001b[<0;1;1m".toByteArray(Charsets.US_ASCII), sink.sent[1])
    }

    @Test
    fun trackingOn_dragSendsPressMoveRelease() {
        val encoder = FakeEncoder(wantsMouse = true)
        val sink = RecordingSink()
        val controller = MouseReportGestureController(encoder, sink)

        controller.onDrag(Offset(1f, 1f), DragPhase.START)
        controller.onDrag(Offset(21f, 1f), DragPhase.MOVE)
        controller.onDrag(Offset(21f, 1f), DragPhase.END)

        assertEquals(
            listOf(MouseAction.PRESS, MouseAction.MOTION, MouseAction.RELEASE),
            encoder.actions,
        )
        assertEquals(3, sink.sent.size)
    }

    @Test
    fun doubleTapInMouseMode_isTwoClicks_notWordSelection() {
        // Double-tap selection is the APP's gesture. When the program owns
        // the touch, two quick taps are two clicks — which is what an `htop`
        // or a text-mode file manager expects.
        val encoder = FakeEncoder(wantsMouse = true)
        val sink = RecordingSink()
        val controller = MouseReportGestureController(encoder, sink)

        controller.onTap(Offset(15f, 25f), taps = 1)
        controller.onTap(Offset(15f, 25f), taps = 2)

        assertEquals(4, sink.sent.size)
        assertEquals(
            listOf(MouseAction.PRESS, MouseAction.RELEASE, MouseAction.PRESS, MouseAction.RELEASE),
            encoder.actions,
        )
    }

    @Test
    fun encoderReturningEmptyArray_producesNoFrame() {
        // Movement within the same cell: the native encoder returns zero
        // bytes. Sending an empty frame to the PTY is not harmless — it is
        // network noise per pixel of drag.
        val sink = RecordingSink()
        val controller = MouseReportGestureController(
            { _, _, _, _ -> ByteArray(0) },
            sink,
        )

        controller.onDrag(Offset(1f, 1f), DragPhase.MOVE)

        assertTrue(sink.sent.isEmpty())
    }

    @Test
    fun moveWithinSameCell_doesNotRepeatReport() {
        // A dragging finger produces one touch event per frame — dozens of
        // them within the same cell. Without deduplication each would become a
        // WebSocket frame and a PTY write, and the repeated report says
        // nothing new: the program already knows where the pointer is.
        //
        // libghostty-vt does NOT do that suppression in mode 1002 even with
        // `TRACK_LAST_CELL` on — measured on the emulator, see
        // `MousePasteEncodingTest.encoderDoesNotDedupMovesInMode1002`.
        val encoder = FakeEncoder(wantsMouse = true)
        val sink = RecordingSink()
        val controller = MouseReportGestureController(encoder, sink)

        controller.onDrag(Offset(10f, 20f), DragPhase.START)
        controller.onDrag(Offset(12f, 22f), DragPhase.MOVE)
        controller.onDrag(Offset(14f, 24f), DragPhase.MOVE)
        controller.onDrag(Offset(18f, 26f), DragPhase.MOVE)

        assertEquals(
            "pressiona + UM movimento, por mais que o dedo ande dentro da célula",
            2,
            sink.sent.size,
        )
    }

    @Test
    fun onCellChange_moveIsReportedAgain() {
        val encoder = FakeEncoder(wantsMouse = true)
        val sink = RecordingSink()
        val controller = MouseReportGestureController(encoder, sink)

        controller.onDrag(Offset(10f, 20f), DragPhase.START)
        controller.onDrag(Offset(12f, 22f), DragPhase.MOVE)
        controller.onDrag(Offset(32f, 22f), DragPhase.MOVE)

        assertEquals(3, sink.sent.size)
        assertArrayEquals("\u001b[<32;2;1M".toByteArray(Charsets.US_ASCII), sink.sent[2])
    }

    @Test
    fun newDrag_doesNotInheritPreviousMemory() {
        // Without this reset, dragging twice in a row to the same cell would
        // make the second drag lose its first movement.
        val encoder = FakeEncoder(wantsMouse = true)
        val sink = RecordingSink()
        val controller = MouseReportGestureController(encoder, sink)

        controller.onDrag(Offset(10f, 20f), DragPhase.START)
        controller.onDrag(Offset(12f, 22f), DragPhase.MOVE)
        controller.onDrag(Offset(12f, 22f), DragPhase.END)
        sink.sent.clear()

        controller.onDrag(Offset(10f, 20f), DragPhase.START)
        controller.onDrag(Offset(12f, 22f), DragPhase.MOVE)

        assertEquals("o movimento do novo arraste tem que sair", 2, sink.sent.size)
    }
}
