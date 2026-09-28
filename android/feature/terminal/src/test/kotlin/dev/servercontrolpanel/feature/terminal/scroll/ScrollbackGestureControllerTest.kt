package dev.servercontrolpanel.feature.terminal.scroll

import androidx.compose.ui.geometry.Offset
import dev.servercontrolpanel.terminalengine.MouseAction
import dev.servercontrolpanel.terminalengine.MouseButton
import dev.servercontrolpanel.terminalengine.MouseGeometry
import dev.servercontrolpanel.terminalengine.TerminalModes
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

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

    @Test
    fun dragSmallerThanCell_doesNotScrollButAccumulates() {
        val r = Recorder()
        val c = controller(r)
        c.onScrollStart()

        drag(c, 8f)
        assertTrue("nothing should have scrolled yet", r.scrolls.isEmpty())
        drag(c, 8f)
        assertTrue(r.scrolls.isEmpty())
        drag(c, 8f)
        assertEquals(listOf(-1), r.scrolls)
    }

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

    @Test
    fun newGesture_resetsAccumulated() {
        val r = Recorder()
        val c = controller(r)
        c.onScrollStart()
        drag(c, 19f)
        c.onScrollEnd()

        c.onScrollStart()
        drag(c, 19f)
        assertTrue("the previous gesture's remainder leaked", r.scrolls.isEmpty())
    }

    @Test
    fun noMouse_onNormalScreen_scrollsLocalViewport() {
        val r = Recorder()
        val c = controller(r)
        c.onScrollStart()
        drag(c, 40f)
        assertEquals(listOf(-2), r.scrolls)
        assertTrue("should not send any bytes to the PTY", r.bytes.isEmpty())
    }

    @Test
    fun mouseActive_dragBecomesWheel_andDoesNotScrollLocally() {
        val r = Recorder()
        r.modes = TerminalModes.NONE.copy(mouseTracking = true)
        val c = controller(r)
        c.onScrollStart()
        drag(c, 60f)

        assertTrue("the local viewport must not move", r.scrolls.isEmpty())
        assertEquals(
            "one wheel event per line, upwards",
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

    @Test
    fun altScreenWithoutAltScroll_doesNothingAndEndsFling() {
        val r = Recorder()
        r.modes = TerminalModes.NONE.copy(altScreen = true, altScroll = false)
        val c = controller(r)
        c.onScrollStart()

        assertFalse("the fling should stop", drag(c, 40f))
        assertTrue(r.scrolls.isEmpty())
        assertTrue(r.bytes.isEmpty())
    }

    @Test
    fun atEndOfHistory_reportsNowhereToGo() {
        val r = Recorder()
        r.hasRoom = false
        val c = controller(r)
        c.onScrollStart()
        assertFalse(drag(c, 40f))
    }

    @Test
    fun mouseActive_flingIsNeverStoppedByLocalEnd() {
        val r = Recorder()
        r.hasRoom = false
        r.modes = TerminalModes.NONE.copy(mouseTracking = true)
        val c = controller(r)
        c.onScrollStart()
        assertTrue(drag(c, 40f))
    }

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
            scrollViewport = { throw AssertionError("must not scroll without geometry") },
            canScrollViewport = { true },
            sendBytes = { throw AssertionError("must not send bytes without geometry") },
            encodeMouse = { _, _, _, _, _ -> null },
        )
        c.onScrollStart()
        assertFalse(c.onScroll(100f, Offset.Zero))
    }
}
