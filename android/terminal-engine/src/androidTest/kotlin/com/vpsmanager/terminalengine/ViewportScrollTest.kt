package com.vpsmanager.terminalengine

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Instrumented, needs the real `.so`. Proves the emulator's history is
 * navigable: moving the viewport changes what [TerminalEngine.snapshot] returns.
 *
 * Also pins library behavior the gesture layer depends on:
 *  - new output does not drag the viewport while the user reads the past;
 *  - the alternate screen has no history to navigate;
 *  - the wheel is xterm's button 4/5, which is how `htop` recognizes it.
 */
class ViewportScrollTest {

    private fun row(snapshot: CellSnapshot, y: Int): String {
        val sb = StringBuilder()
        for (x in 0 until snapshot.cols) {
            val cp = snapshot.cellAt(x, y).codepoint
            if (cp != 0) sb.appendCodePoint(cp)
        }
        return sb.toString().trimEnd()
    }

    /** Writes [lines] numbered lines to become history. */
    private fun fillHistory(engine: TerminalEngine, lines: Int) {
        val sb = StringBuilder()
        for (i in 1..lines) sb.append("linha-").append(i).append("\r\n")
        engine.write(sb.toString().toByteArray(Charsets.UTF_8))
    }

    @Test
    fun notScrolled_snapshotShowsEndAsAlways() {
        val engine = TerminalEngine.create(cols = 40, rows = 10)
        try {
            fillHistory(engine, 100)
            val snap = engine.snapshot()
            // End of history: the last lines written are in view.
            assertTrue(
                "expected the last lines on screen, got: ${row(snap, 0)}",
                (0 until snap.rows).any { row(snap, it) == "linha-100" },
            )
            assertTrue(engine.scrollState().atEnd)
        } finally {
            engine.close()
        }
    }

    @Test
    fun scrollUp_showsRowsThatHadLeftScreen() {
        val engine = TerminalEngine.create(cols = 40, rows = 10)
        try {
            fillHistory(engine, 100)
            val before = (0 until 10).map { row(engine.snapshot(), it) }

            engine.scrollViewport(-50)

            val after = (0 until 10).map { row(engine.snapshot(), it) }
            assertNotEquals("the viewport did not move", before, after)
            // 100 lines written, 10 visible, up 50: we land in the 40s.
            assertTrue(
                "expected past lines, got: $after",
                after.any { it.startsWith("linha-4") || it.startsWith("linha-5") },
            )
        } finally {
            engine.close()
        }
    }

    @Test
    fun scrollToTop_andBackToEnd_areSymmetric() {
        val engine = TerminalEngine.create(cols = 40, rows = 10)
        try {
            fillHistory(engine, 100)
            val atEnd = (0 until 10).map { row(engine.snapshot(), it) }

            engine.scrollToTop()
            val atTop = (0 until 10).map { row(engine.snapshot(), it) }
            assertNotEquals(atEnd, atTop)
            assertFalse(engine.scrollState().atEnd)
            assertEquals(0L, engine.scrollState().offset)

            engine.scrollToBottom()
            assertEquals(atEnd, (0 until 10).map { row(engine.snapshot(), it) })
            assertTrue(engine.scrollState().atEnd)
        } finally {
            engine.close()
        }
    }

    @Test
    fun scrollState_describesPositionInHistory() {
        val engine = TerminalEngine.create(cols = 40, rows = 10)
        try {
            fillHistory(engine, 100)
            val end = engine.scrollState()
            assertTrue("with history there must be something to scroll", end.canScroll)
            assertEquals(10L, end.visible)
            assertTrue("total must include the history", end.total > 10)
            assertTrue(end.atEnd)
            assertEquals(1f, end.progress, 0.001f)

            engine.scrollToTop()
            val top = engine.scrollState()
            assertEquals(0L, top.offset)
            assertFalse(top.atEnd)
            assertEquals(0f, top.progress, 0.001f)
            // The total does not change just because we looked back.
            assertEquals(end.total, top.total)
        } finally {
            engine.close()
        }
    }

    /** A position read back returns to the engine unconverted (same line space). */
    @Test
    fun scrollToRow_roundTripsWithReadOffset() {
        val engine = TerminalEngine.create(cols = 40, rows = 10)
        try {
            fillHistory(engine, 100)
            engine.scrollViewport(-37)
            val target = engine.scrollState().offset
            val expected = (0 until 10).map { row(engine.snapshot(), it) }

            engine.scrollToBottom()
            engine.scrollToRow(target)

            assertEquals(target, engine.scrollState().offset)
            assertEquals(expected, (0 until 10).map { row(engine.snapshot(), it) })
        } finally {
            engine.close()
        }
    }

    /** New output arriving while reading the past must not drag the screen down. */
    @Test
    fun newOutput_doesNotDragViewport_whileReadingPast() {
        val engine = TerminalEngine.create(cols = 40, rows = 10)
        try {
            fillHistory(engine, 100)
            engine.scrollViewport(-50)
            val reading = (0 until 10).map { row(engine.snapshot(), it) }
            val offsetBefore = engine.scrollState().offset

            engine.write("intruder-1\r\nintruder-2\r\nintruder-3\r\n".toByteArray(Charsets.UTF_8))

            assertEquals(
                "the screen jumped to the end by itself when new output arrived",
                reading,
                (0 until 10).map { row(engine.snapshot(), it) },
            )
            assertFalse(engine.scrollState().atEnd)
            // The offset grows with the history to keep the SAME lines in view.
            assertTrue(engine.scrollState().offset >= offsetBefore)
        } finally {
            engine.close()
        }
    }

    @Test
    fun pinnedToEnd_newOutputKeepsFollowing() {
        val engine = TerminalEngine.create(cols = 40, rows = 10)
        try {
            fillHistory(engine, 100)
            engine.write("recem-chegada\r\n".toByteArray(Charsets.UTF_8))
            val snap = engine.snapshot()
            assertTrue(
                "pinned to the end, new output must appear",
                (0 until snap.rows).any { row(snap, it) == "recem-chegada" },
            )
            assertTrue(engine.scrollState().atEnd)
        } finally {
            engine.close()
        }
    }

    private fun enterAltScreen(engine: TerminalEngine) {
        engine.write("\u001b[?1049h".toByteArray(Charsets.UTF_8))
    }

    @Test
    fun altScreen_isReportedInModes() {
        val engine = TerminalEngine.create(cols = 40, rows = 10)
        try {
            assertFalse(engine.modes().altScreen)
            enterAltScreen(engine)
            assertTrue("1049h must turn altScreen on", engine.modes().altScreen)
            engine.write("\u001b[?1049l".toByteArray(Charsets.UTF_8))
            assertFalse(engine.modes().altScreen)
        } finally {
            engine.close()
        }
    }

    /** The alternate screen has no history; the library pins the viewport there. */
    @Test
    fun altScreen_hasNoHistoryToBrowse() {
        val engine = TerminalEngine.create(cols = 40, rows = 10)
        try {
            fillHistory(engine, 100)
            enterAltScreen(engine)
            engine.write("full-screen".toByteArray(Charsets.UTF_8))

            val before = (0 until 10).map { row(engine.snapshot(), it) }
            engine.scrollViewport(-50)
            assertEquals(
                "the viewport must not move on the alternate screen",
                before,
                (0 until 10).map { row(engine.snapshot(), it) },
            )
            assertTrue(engine.scrollState().atEnd)
            assertFalse("the alternate screen has no history", engine.scrollState().canScroll)
        } finally {
            engine.close()
        }
    }

    /**
     * `less` and `man` do not enable 1007 themselves, so scrolling in them
     * relies on the terminal's default being ON.
     */
    @Test
    fun altScroll_startsOn_likeXterm() {
        val engine = TerminalEngine.create(cols = 40, rows = 10)
        try {
            assertTrue("1007 must start on", engine.modes().altScroll)
        } finally {
            engine.close()
        }
    }

    /** DECSET 1007 and DECCKM reach the gesture layer as the emulator's truth. */
    @Test
    fun altScroll_andCursorKeys_areTrackedByEmulator() {
        val engine = TerminalEngine.create(cols = 40, rows = 10)
        try {
            engine.write("\u001b[?1007h".toByteArray(Charsets.UTF_8))
            assertTrue("1007h must turn altScroll on", engine.modes().altScroll)
            engine.write("\u001b[?1007l".toByteArray(Charsets.UTF_8))
            assertFalse(engine.modes().altScroll)

            assertFalse(engine.modes().cursorKeysApplication)
            engine.write("\u001b[?1h".toByteArray(Charsets.UTF_8))
            assertTrue("DECCKM on must be reported", engine.modes().cursorKeysApplication)
            engine.write("\u001b[?1l".toByteArray(Charsets.UTF_8))
            assertFalse(engine.modes().cursorKeysApplication)
        } finally {
            engine.close()
        }
    }

    /** The wheel is xterm's button 4/5; otherwise `htop` does not recognize scrolling. */
    @Test
    fun wheel_isEncodedAsButtons4And5_whenProgramAsksForMouse() {
        val engine = TerminalEngine.create(cols = 40, rows = 10)
        try {
            val geometry = MouseGeometry(
                cellWidthPx = 10,
                cellHeightPx = 20,
                screenWidthPx = 400,
                screenHeightPx = 200,
            )
            // No tracking: no bytes, as at any shell prompt.
            assertTrue(
                engine.encodeMouse(
                    MouseAction.PRESS, MouseButton.WHEEL_UP, 5f, 5f, geometry,
                ) == null,
            )

            // The program requests mouse (1000) in SGR format (1006).
            engine.write("\u001b[?1000h\u001b[?1006h".toByteArray(Charsets.UTF_8))
            assertTrue(engine.modes().mouseTracking)

            val up = engine.encodeMouse(MouseAction.PRESS, MouseButton.WHEEL_UP, 5f, 5f, geometry)
            val down = engine.encodeMouse(MouseAction.PRESS, MouseButton.WHEEL_DOWN, 5f, 5f, geometry)
            val upText = up?.toString(Charsets.US_ASCII)
            val downText = down?.toString(Charsets.US_ASCII)

            assertTrue("wheel up produced no report", upText != null)
            assertTrue("wheel down produced no report", downText != null)
            // SGR: ESC [ < 64 ; col ; line M for wheel up, 65 for wheel down.
            assertTrue("expected button 64 (wheel up), got: $upText", upText!!.contains("<64;"))
            assertTrue("expected button 65 (wheel down), got: $downText", downText!!.contains("<65;"))
        } finally {
            engine.close()
        }
    }
}
