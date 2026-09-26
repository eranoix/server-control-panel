package com.vpsmanager.terminalengine

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Instrumented — needs the real `.so`. Proves that the emulator's history,
 * which always existed (`max_scrollback`) and was **unreachable**, is now
 * navigable: moving the viewport changes what [TerminalEngine.snapshot]
 * hands back.
 *
 * It also pins here the answers only the library could give, and which decide
 * the design of the gesture further up:
 *  - new output does **not** drag the viewport while the owner is reading the past;
 *  - the alternate screen has no history at all to navigate;
 *  - the wheel is xterm's button 4/5, and that is how `htop` recognizes it.
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

    /** Writes `linhas` numbered lines, one per line, to become history. */
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
                "esperava as últimas linhas na tela, veio: ${row(snap, 0)}",
                (0 until snap.rows).any { row(snap, it) == "linha-100" },
            )
            assertTrue(engine.scrollState().atEnd)
        } finally {
            engine.close()
        }
    }

    /**
     * The test that failed before [TerminalEngine.scrollViewport] existed:
     * there was no way to ask for the past, and the snapshot was eternally the
     * live screen.
     */
    @Test
    fun scrollUp_showsRowsThatHadLeftScreen() {
        val engine = TerminalEngine.create(cols = 40, rows = 10)
        try {
            fillHistory(engine, 100)
            val before = (0 until 10).map { row(engine.snapshot(), it) }

            engine.scrollViewport(-50)

            val after = (0 until 10).map { row(engine.snapshot(), it) }
            assertNotEquals("o viewport não se moveu", before, after)
            // 100 lines written, 10 visible, we go up 50: we land in the 40s.
            assertTrue(
                "esperava linhas do passado, veio: $after",
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
            assertTrue("sem histórico não há o que rolar", end.canScroll)
            assertEquals(10L, end.visible)
            assertTrue("total deve conter o histórico", end.total > 10)
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

    /** The position read goes back to the engine with no conversion — the same line space. */
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

    /**
     * The requirement that matters most to whoever is reading: new output
     * arriving while looking at the past must **not** drag the screen down.
     */
    @Test
    fun newOutput_doesNotDragViewport_whileReadingPast() {
        val engine = TerminalEngine.create(cols = 40, rows = 10)
        try {
            fillHistory(engine, 100)
            engine.scrollViewport(-50)
            val reading = (0 until 10).map { row(engine.snapshot(), it) }
            val offsetBefore = engine.scrollState().offset

            engine.write("intrusa-1\r\nintrusa-2\r\nintrusa-3\r\n".toByteArray(Charsets.UTF_8))

            assertEquals(
                "a tela saltou sozinha para o fim ao chegar saída nova",
                reading,
                (0 until 10).map { row(engine.snapshot(), it) },
            )
            assertFalse(engine.scrollState().atEnd)
            // The offset follows the growth of the history so as to keep the
            // SAME lines in view: it is the content that must not change.
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
                "preso no fim, a saída nova tem que aparecer",
                (0 until snap.rows).any { row(snap, it) == "recem-chegada" },
            )
            assertTrue(engine.scrollState().atEnd)
        } finally {
            engine.close()
        }
    }

    // ---- Tela alternativa: o caso que quebra ------------------------------

    private fun enterAltScreen(engine: TerminalEngine) {
        engine.write("\u001b[?1049h".toByteArray(Charsets.UTF_8))
    }

    @Test
    fun altScreen_isReportedInModes() {
        val engine = TerminalEngine.create(cols = 40, rows = 10)
        try {
            assertFalse(engine.modes().altScreen)
            enterAltScreen(engine)
            assertTrue("1049h tem que acender altScreen", engine.modes().altScreen)
            engine.write("\u001b[?1049l".toByteArray(Charsets.UTF_8))
            assertFalse(engine.modes().altScreen)
        } finally {
            engine.close()
        }
    }

    /**
     * There is no history on the alternate screen — the library itself pins
     * the viewport there. That is why the gesture must not pretend to navigate
     * anything.
     */
    @Test
    fun altScreen_hasNoHistoryToBrowse() {
        val engine = TerminalEngine.create(cols = 40, rows = 10)
        try {
            fillHistory(engine, 100)
            enterAltScreen(engine)
            engine.write("tela-cheia".toByteArray(Charsets.UTF_8))

            val before = (0 until 10).map { row(engine.snapshot(), it) }
            engine.scrollViewport(-50)
            assertEquals(
                "o viewport não podia se mover na tela alternativa",
                before,
                (0 until 10).map { row(engine.snapshot(), it) },
            )
            assertTrue(engine.scrollState().atEnd)
            assertFalse("tela alternativa não tem histórico", engine.scrollState().canScroll)
        } finally {
            engine.close()
        }
    }

    /**
     * The DEFAULT for 1007 decides the real world: `less` and `man` enter the
     * alternate screen but do NOT turn 1007 on — the one that turns it on by
     * default is the terminal. If it were born off, scrolling inside `less`
     * would do nothing.
     */
    @Test
    fun altScroll_startsOn_likeXterm() {
        val engine = TerminalEngine.create(cols = 40, rows = 10)
        try {
            assertTrue("1007 tem que nascer ligada", engine.modes().altScroll)
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
            assertTrue("1007h tem que acender altScroll", engine.modes().altScroll)
            engine.write("\u001b[?1007l".toByteArray(Charsets.UTF_8))
            assertFalse(engine.modes().altScroll)

            assertFalse(engine.modes().cursorKeysApplication)
            engine.write("\u001b[?1h".toByteArray(Charsets.UTF_8))
            assertTrue("DECCKM ligado tem que ser reportado", engine.modes().cursorKeysApplication)
            engine.write("\u001b[?1l".toByteArray(Charsets.UTF_8))
            assertFalse(engine.modes().cursorKeysApplication)
        } finally {
            engine.close()
        }
    }

    /** The wheel is xterm's button 4/5 — without that `htop` does not recognize scrolling. */
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
            // Sem rastreamento: nenhum byte, como em qualquer prompt de shell.
            assertTrue(
                engine.encodeMouse(
                    MouseAction.PRESS, MouseButton.WHEEL_UP, 5f, 5f, geometry,
                ) == null,
            )

            // O programa pede mouse (1000) em formato SGR (1006).
            engine.write("\u001b[?1000h\u001b[?1006h".toByteArray(Charsets.UTF_8))
            assertTrue(engine.modes().mouseTracking)

            val up = engine.encodeMouse(MouseAction.PRESS, MouseButton.WHEEL_UP, 5f, 5f, geometry)
            val down = engine.encodeMouse(MouseAction.PRESS, MouseButton.WHEEL_DOWN, 5f, 5f, geometry)
            val upText = up?.toString(Charsets.US_ASCII)
            val downText = down?.toString(Charsets.US_ASCII)

            assertTrue("roda para cima não produziu relatório", upText != null)
            assertTrue("roda para baixo não produziu relatório", downText != null)
            // SGR: ESC [ < 64 ; col ; line M for the wheel up, 65 for the wheel down.
            assertTrue("esperava botão 64 (roda cima), veio: $upText", upText!!.contains("<64;"))
            assertTrue("esperava botão 65 (roda baixo), veio: $downText", downText!!.contains("<65;"))
        } finally {
            engine.close()
        }
    }
}
