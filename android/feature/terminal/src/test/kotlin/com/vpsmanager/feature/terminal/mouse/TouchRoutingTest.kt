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
 * Fake encoder that, like the real one, returns `null` until the remote program asks
 * for mouse tracking. The real encoder is covered by `MousePasteEncodingTest` in
 * `:terminal-engine`; this tests the controller's reaction.
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
 * Mouse reports go out only when the remote program asked for tracking (DECSET
 * 1000/1002/1003); otherwise they reach the shell as garbage text. When requested,
 * the bytes must be what the program expects.
 */
class TouchRoutingTest {

    @Test
    fun noProgramAskingForMouse_gestureAlwaysBelongsToApp() {
        val routing = TouchRouting { false }

        assertFalse("there is no mouse to report", routing.programWantsMouse())
        assertTrue("the gesture belongs to the app (selection and keyboard)", routing.tapBelongsToApp())
    }

    @Test
    fun programAskingForMouse_gestureBelongsToProgram() {
        val routing = TouchRouting { true }

        assertTrue(routing.programWantsMouse())
        assertFalse("inside htop a tap is a click, not a selection", routing.tapBelongsToApp())
    }

    @Test
    fun noPreferenceCanOverrideRemoteProgram() {
        // Ownership depends only on whether the remote program asked for the mouse;
        // no app preference may contradict it. Selecting inside a full-screen
        // program uses a long press, which is handled before routing.
        val requesting = TouchRouting { true }
        val notRequesting = TouchRouting { false }

        assertEquals(
            "the gesture owner depends only on the emulator state",
            listOf(false, true),
            listOf(requesting.tapBelongsToApp(), notRequesting.tapBelongsToApp()),
        )
    }

    @Test
    fun stateIsReadOnEachQuery_notRemembered() {
        // Programs toggle tracking without notifying the app, so it must re-read.
        var requesting = false
        val routing = TouchRouting { requesting }

        assertTrue(routing.tapBelongsToApp())
        requesting = true
        assertFalse("htop opened, so the tap belongs to it", routing.tapBelongsToApp())
        requesting = false
        assertTrue("htop exited, so the tap belongs to the app again", routing.tapBelongsToApp())
    }

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
            "with no active tracking no bytes may reach the PTY (they would show as garbage on the command line)",
            sink.sent.isEmpty(),
        )
        assertFalse("but the encoder was consulted, since it decides", encoder.actions.isEmpty())
    }

    @Test
    fun trackingOn_tapBecomesPressReleasePair() {
        val encoder = FakeEncoder(wantsMouse = true)
        val sink = RecordingSink()
        val controller = MouseReportGestureController(encoder, sink)

        controller.onTap(Offset(15f, 25f), taps = 1)

        assertEquals("a click is press plus release", 2, sink.sent.size)
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
        // When the program owns the touch, two quick taps are two clicks, not a word
        // selection.
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
        // The native encoder returns zero bytes for movement within a cell; sending
        // empty frames would be network noise.
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
        // Deduplicate moves within a cell ourselves: libghostty-vt does not in mode
        // 1002 (see `MousePasteEncodingTest.encoderDoesNotDedupMovesInMode1002`).
        val encoder = FakeEncoder(wantsMouse = true)
        val sink = RecordingSink()
        val controller = MouseReportGestureController(encoder, sink)

        controller.onDrag(Offset(10f, 20f), DragPhase.START)
        controller.onDrag(Offset(12f, 22f), DragPhase.MOVE)
        controller.onDrag(Offset(14f, 24f), DragPhase.MOVE)
        controller.onDrag(Offset(18f, 26f), DragPhase.MOVE)

        assertEquals(
            "press plus one move, however far the finger moves within the cell",
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
        // Without a reset, a second drag to the same cell would lose its first move.
        val encoder = FakeEncoder(wantsMouse = true)
        val sink = RecordingSink()
        val controller = MouseReportGestureController(encoder, sink)

        controller.onDrag(Offset(10f, 20f), DragPhase.START)
        controller.onDrag(Offset(12f, 22f), DragPhase.MOVE)
        controller.onDrag(Offset(12f, 22f), DragPhase.END)
        sink.sent.clear()

        controller.onDrag(Offset(10f, 20f), DragPhase.START)
        controller.onDrag(Offset(12f, 22f), DragPhase.MOVE)

        assertEquals("the new drag's move must be sent", 2, sink.sent.size)
    }
}
