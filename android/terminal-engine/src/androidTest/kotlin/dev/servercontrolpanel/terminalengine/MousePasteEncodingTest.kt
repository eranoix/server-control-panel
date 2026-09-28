package dev.servercontrolpanel.terminalengine

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.runner.RunWith
import org.junit.Test
import androidx.test.ext.junit.runners.AndroidJUnit4

@RunWith(AndroidJUnit4::class)
class MousePasteEncodingTest {

    private val geometry = MouseGeometry(
        cellWidthPx = 20,
        cellHeightPx = 40,
        screenWidthPx = 80 * 20,
        screenHeightPx = 24 * 40,
    )

    private fun engine(): TerminalEngine = TerminalEngine.create(cols = 80, rows = 24)

    private fun TerminalEngine.feed(vt: String) = write(vt.toByteArray(Charsets.US_ASCII))

    private fun center(col: Int, row: Int): Pair<Float, Float> =
        (col * 20f + 10f) to (row * 40f + 20f)

    @Test
    fun trackingOff_tapEmitsNoBytes() {
        val engine = engine()
        try {
            assertFalse("bash at a plain prompt does not request the mouse", engine.modes().mouseTracking)

            val (x, y) = center(col = 27, row = 14)
            val press = engine.encodeMouse(MouseAction.PRESS, MouseButton.LEFT, x, y, geometry, anyButtonPressed = true)
            val release = engine.encodeMouse(MouseAction.RELEASE, MouseButton.LEFT, x, y, geometry)

            assertNull("without tracking, a tap must produce no bytes", press)
            assertNull("nor the release", release)
        } finally {
            engine.close()
        }
    }

    @Test
    fun sgrTrackingOn_emitsCorrectSgrSequence() {
        val engine = engine()
        try {
            engine.feed("\u001b[?1000h\u001b[?1006h")
            assertTrue("the program enabled tracking", engine.modes().mouseTracking)

            val (x, y) = center(col = 4, row = 9)
            val press = engine.encodeMouse(MouseAction.PRESS, MouseButton.LEFT, x, y, geometry, anyButtonPressed = true)
            val release = engine.encodeMouse(MouseAction.RELEASE, MouseButton.LEFT, x, y, geometry)

            assertArrayEquals("\u001b[<0;5;10M".toByteArray(Charsets.US_ASCII), press)
            assertArrayEquals("\u001b[<0;5;10m".toByteArray(Charsets.US_ASCII), release)
        } finally {
            engine.close()
        }
    }

    @Test
    fun programTurnsTrackingOff_goesBackToEmittingNothing() {
        val engine = engine()
        try {
            engine.feed("\u001b[?1000h\u001b[?1006h")
            val (x, y) = center(col = 1, row = 1)
            assertTrue(engine.encodeMouse(MouseAction.PRESS, MouseButton.LEFT, x, y, geometry, anyButtonPressed = true) != null)

            engine.feed("\u001b[?1000l")

            assertFalse(engine.modes().mouseTracking)
            assertNull(
                "with tracking off, a tap has no recipient again",
                engine.encodeMouse(MouseAction.PRESS, MouseButton.LEFT, x, y, geometry, anyButtonPressed = true),
            )
        } finally {
            engine.close()
        }
    }

    @Test
    fun x10Format_doesNotEmitSgr() {
        val engine = engine()
        try {
            engine.feed("\u001b[?1000h")

            val (x, y) = center(col = 4, row = 9)
            val press = engine.encodeMouse(MouseAction.PRESS, MouseButton.LEFT, x, y, geometry, anyButtonPressed = true)

            val expected = byteArrayOf(
                0x1b, '['.code.toByte(), 'M'.code.toByte(),
                (32 + 0).toByte(), (32 + 5).toByte(), (32 + 10).toByte(),
            )
            assertArrayEquals(expected, press)
        } finally {
            engine.close()
        }
    }

    @Test
    fun moveDuringDrag_reportsCellUnderFinger() {
        val engine = engine()
        try {
            engine.feed("\u001b[?1002h\u001b[?1006h")
            val (x, y) = center(col = 3, row = 3)

            engine.encodeMouse(MouseAction.PRESS, MouseButton.LEFT, x, y, geometry, anyButtonPressed = true)
            val sameCell = engine.encodeMouse(
                MouseAction.MOTION, MouseButton.LEFT, x + 2f, y + 2f, geometry, anyButtonPressed = true,
            )
            val otherCell = engine.encodeMouse(
                MouseAction.MOTION, MouseButton.LEFT, x + 20f, y, geometry, anyButtonPressed = true,
            )

            assertArrayEquals("\u001b[<32;4;4M".toByteArray(Charsets.US_ASCII), sameCell)
            assertArrayEquals("\u001b[<32;5;4M".toByteArray(Charsets.US_ASCII), otherCell)
        } finally {
            engine.close()
        }
    }

    @Test
    fun encoderDoesNotDedupMovesInMode1002() {
        val engine = engine()
        try {
            engine.feed("\u001b[?1002h\u001b[?1006h")
            val (x, y) = center(col = 3, row = 3)
            engine.encodeMouse(MouseAction.PRESS, MouseButton.LEFT, x, y, geometry, anyButtonPressed = true)

            val first = engine.encodeMouse(
                MouseAction.MOTION, MouseButton.LEFT, x + 2f, y + 2f, geometry, anyButtonPressed = true,
            )
            val second = engine.encodeMouse(
                MouseAction.MOTION, MouseButton.LEFT, x + 4f, y + 4f, geometry, anyButtonPressed = true,
            )

            assertArrayEquals(
                "no dedup in the library: the second move repeats the first",
                first,
                second,
            )
        } finally {
            engine.close()
        }
    }

    @Test
    fun pasteWithoutDecset2004_hasNoMarkersAndNewlineBecomesReturn() {
        val engine = engine()
        try {
            assertFalse(engine.modes().bracketedPaste)

            val bytes = engine.encodePaste("one\ntwo")

            val text = String(bytes, Charsets.UTF_8)
            assertFalse("without 2004 the markers would be literal text on the line", text.contains("\u001b[200~"))
            assertFalse(text.contains("\u001b[201~"))
            assertEquals("one\rtwo", text)
        } finally {
            engine.close()
        }
    }

    @Test
    fun pasteWithDecset2004_isWrappedInMarkers() {
        val engine = engine()
        try {
            engine.feed("\u001b[?2004h")
            assertTrue("the program enabled bracketed paste", engine.modes().bracketedPaste)

            val text = String(engine.encodePaste("one\ntwo"), Charsets.UTF_8)

            assertEquals("\u001b[200~one\ntwo\u001b[201~", text)
        } finally {
            engine.close()
        }
    }

    @Test
    fun pasteWithEmbeddedEndMarker_doesNotLetTextBecomeCommand() {
        val engine = engine()
        try {
            engine.feed("\u001b[?2004h")

            val text = String(engine.encodePaste("harmless\u001b[201~rm -rf /"), Charsets.UTF_8)

            assertTrue("the wrapper starts and ends where it should", text.startsWith("\u001b[200~"))
            assertTrue(text.endsWith("\u001b[201~"))
            val inner = text.removePrefix("\u001b[200~").removeSuffix("\u001b[201~")
            assertFalse("no end marker may survive inside", inner.contains("\u001b[201~"))
            assertFalse("no raw ESC may survive inside", inner.contains("\u001b"))
        } finally {
            engine.close()
        }
    }

    @Test
    fun modes_areIndependentOfEachOther() {
        val engine = engine()
        try {
            assertEquals(TerminalModes.NONE, engine.modes())

            engine.feed("\u001b[?2004h")
            assertEquals(TerminalModes(mouseTracking = false, bracketedPaste = true), engine.modes())

            engine.feed("\u001b[?1003h")
            assertEquals(TerminalModes(mouseTracking = true, bracketedPaste = true), engine.modes())

            engine.feed("\u001b[?2004l")
            assertEquals(TerminalModes(mouseTracking = true, bracketedPaste = false), engine.modes())
        } finally {
            engine.close()
        }
    }
}
