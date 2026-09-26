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

    private val cellHeight = 20

    private class Recorder {
        var modes = TerminalModes.NONE
        val scrolls = mutableListOf<Int>()
        val bytes = mutableListOf<ByteArray>()
        val wheels = mutableListOf<MouseButton>()
        var hasRoom = true
    }

    private fun controller(r: Recorder): ScrollbackGestureController =
        ScrollbackGestureController(
            modes = { r.modes },
            geometry = {
                MouseGeometry(
                    cellWidthPx = 10,
                    cellHeightPx = cellHeight,
                    screenWidthPx = 400,
                    screenHeightPx = 200,
                )
            },
            scrollViewport = { r.scrolls += it },
            canScrollViewport = { r.hasRoom },
            sendBytes = { r.bytes += it },
            encodeMouse = { _, button, _, _, _ ->
                r.wheels += button
                byteArrayOf(button.ordinal.toByte())
            },
        )

    private fun drag(c: ScrollbackGestureController, px: Float): Boolean {
        return c.onScroll(px, Offset(5f, 5f))
    }

    // ---- pixels become lines --------------------------------------------

    /**
     * Less than one cell scrolls nothing — but the pixel is not lost, and that
     * is what makes the content follow the finger instead of jumping three
     * lines at a time.
     */
    @Test
    fun dragSmallerThanCell_doesNotScrollButAccumulates() {
        val r = Recorder()
        val c = controller(r)
        c.onScrollStart()

        drag(c, 8f)
        assertTrue("nada devia ter rolado ainda", r.scrolls.isEmpty())
        drag(c, 8f)
        assertTrue(r.scrolls.isEmpty())
        // 8+8+8 = 24 px, past one cell of 20.
        drag(c, 8f)
        assertEquals(listOf(-1), r.scrolls)
    }

    /** Finger DOWN shows the PAST — negative, the same convention as the wheel. */
    @Test
    fun fingerDown_scrollsToPast() {
        val r = Recorder()
        val c = controller(r)
        c.onScrollStart()
        drag(c, 60f)
        assertEquals(listOf(-3), r.scrolls)
    }

    @Test
    fun fingerUp_scrollsToPresent() {
        val r = Recorder()
        val c = controller(r)
        c.onScrollStart()
        drag(c, -40f)
        assertEquals(listOf(2), r.scrolls)
    }

    /** Starting a new gesture clears the remainder: the previous drag does not leak. */
    @Test
    fun newGesture_resetsAccumulated() {
        val r = Recorder()
        val c = controller(r)
        c.onScrollStart()
        drag(c, 19f)
        c.onScrollEnd()

        c.onScrollStart()
        drag(c, 19f)
        assertTrue("a sobra do gesto anterior vazou", r.scrolls.isEmpty())
    }

    // ---- where the lines go ---------------------------------------------

    @Test
    fun noMouse_onNormalScreen_scrollsLocalViewport() {
        val r = Recorder()
        val c = controller(r)
        c.onScrollStart()
        drag(c, 40f)
        assertEquals(listOf(-2), r.scrolls)
        assertTrue("não devia mandar byte nenhum ao PTY", r.bytes.isEmpty())
    }

    /** With `htop` open, the drag becomes the wheel — and nothing scrolls locally. */
    @Test
    fun mouseActive_dragBecomesWheel_andDoesNotScrollLocally() {
        val r = Recorder()
        r.modes = TerminalModes.NONE.copy(mouseTracking = true)
        val c = controller(r)
        c.onScrollStart()
        drag(c, 60f)

        assertTrue("o viewport local não podia se mover", r.scrolls.isEmpty())
        assertEquals(
            "uma roda por linha, para cima",
            listOf(MouseButton.WHEEL_UP, MouseButton.WHEEL_UP, MouseButton.WHEEL_UP),
            r.wheels,
        )
        assertEquals(3, r.bytes.size)
    }

    @Test
    fun mouseActive_fingerUp_sendsWheelDown() {
        val r = Recorder()
        r.modes = TerminalModes.NONE.copy(mouseTracking = true)
        val c = controller(r)
        c.onScrollStart()
        drag(c, -40f)
        assertEquals(listOf(MouseButton.WHEEL_DOWN, MouseButton.WHEEL_DOWN), r.wheels)
    }

    /** `less`: alternate screen, no mouse, 1007 on — the drag becomes arrow keys. */
    @Test
    fun altScreenWithAltScroll_sendsArrows() {
        val r = Recorder()
        r.modes = TerminalModes.NONE.copy(altScreen = true, altScroll = true)
        val c = controller(r)
        c.onScrollStart()
        drag(c, 40f)

        assertTrue(r.scrolls.isEmpty())
        assertEquals(1, r.bytes.size)
        assertArrayEquals("\u001b[A\u001b[A".toByteArray(), r.bytes.single())
    }

    @Test
    fun altScreenWithDECCKM_sendsApplicationModeArrows() {
        val r = Recorder()
        r.modes = TerminalModes.NONE.copy(
            altScreen = true,
            altScroll = true,
            cursorKeysApplication = true,
        )
        val c = controller(r)
        c.onScrollStart()
        drag(c, 20f)
        assertArrayEquals("\u001bOA".toByteArray(), r.bytes.single())
    }

    /** Full screen without 1007: nothing happens, and the gesture says it is over. */
    @Test
    fun altScreenWithoutAltScroll_doesNothingAndEndsFling() {
        val r = Recorder()
        r.modes = TerminalModes.NONE.copy(altScreen = true, altScroll = false)
        val c = controller(r)
        c.onScrollStart()

        assertFalse("a inércia tinha que parar", drag(c, 40f))
        assertTrue(r.scrolls.isEmpty())
        assertTrue(r.bytes.isEmpty())
    }

    // ---- end of the history ----------------------------------------------

    /**
     * It reached the top: the gesture answers `false` and the fling stops,
     * instead of grinding against the wall until the deceleration curve runs
     * out on its own.
     */
    @Test
    fun atEndOfHistory_reportsNowhereToGo() {
        val r = Recorder()
        r.hasRoom = false
        val c = controller(r)
        c.onScrollStart()
        assertFalse(drag(c, 40f))
    }

    /** The wheel belongs to the remote program: there is no end of OUR history there. */
    @Test
    fun mouseActive_flingIsNeverStoppedByLocalEnd() {
        val r = Recorder()
        r.hasRoom = false
        r.modes = TerminalModes.NONE.copy(mouseTracking = true)
        val c = controller(r)
        c.onScrollStart()
        assertTrue(drag(c, 40f))
    }

    /** An absurd drag must not turn into hundreds of wheel events on the PTY. */
    @Test
    fun absurdDrag_hasWheelCapPerEvent() {
        val r = Recorder()
        r.modes = TerminalModes.NONE.copy(mouseTracking = true)
        val c = controller(r)
        c.onScrollStart()
        drag(c, 20f * 500)
        assertEquals(10, r.wheels.size)
    }

    @Test
    fun noGeometry_doesNothing() {
        val c = ScrollbackGestureController(
            modes = { TerminalModes.NONE },
            geometry = { null },
            scrollViewport = { throw AssertionError("não podia rolar sem geometria") },
            canScrollViewport = { true },
            sendBytes = { throw AssertionError("não podia mandar byte sem geometria") },
            encodeMouse = { _, _, _, _, _ -> null },
        )
        c.onScrollStart()
        assertFalse(c.onScroll(100f, Offset.Zero))
    }
}
