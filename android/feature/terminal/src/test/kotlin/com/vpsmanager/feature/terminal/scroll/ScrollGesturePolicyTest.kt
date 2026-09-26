package com.vpsmanager.feature.terminal.scroll

import com.vpsmanager.terminalengine.TerminalModes
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * The decision table for the vertical drag. This is where it is easy to be
 * silently wrong — scrolling the local history inside an `htop` raises no
 * error, it just does not work — so every row of the table has its own test.
 */
class ScrollGesturePolicyTest {

    private val shell = TerminalModes.NONE

    @Test
    fun normalScreenWithoutMouse_scrollsLocalHistory() {
        assertEquals(ScrollAction.Viewport(-3), decideScroll(shell, -3))
        assertEquals(ScrollAction.Viewport(5), decideScroll(shell, 5))
    }

    /** `htop`, `vim` with mouse, `less`: the gesture belongs to the program, not to us. */
    @Test
    fun programAskedForMouse_becomesWheel_evenOnNormalScreen() {
        val comMouse = shell.copy(mouseTracking = true)
        assertEquals(ScrollAction.Wheel(-3), decideScroll(comMouse, -3))
    }

    @Test
    fun programAskedForMouse_becomesWheel_alsoOnAltScreen() {
        val htop = shell.copy(mouseTracking = true, altScreen = true, altScroll = true)
        assertEquals(ScrollAction.Wheel(-2), decideScroll(htop, -2))
    }

    /** `less`/`man`: alternate screen, no mouse, 1007 on (the default). */
    @Test
    fun altScreenWithAltScroll_becomesArrow() {
        val less = shell.copy(altScreen = true, altScroll = true)
        assertEquals(ScrollAction.Arrows(-4), decideScroll(less, -4))
    }

    /**
     * Alternate screen without 1007 and without mouse: there is no history and
     * the program has declared it does not want a wheel. Doing nothing is the
     * right answer — faking movement here would be lying.
     */
    @Test
    fun altScreenWithoutAltScroll_doesNothing() {
        val full = shell.copy(altScreen = true, altScroll = false)
        assertEquals(ScrollAction.Nothing, decideScroll(full, -4))
    }

    @Test
    fun zeroRowDrag_doesNothing() {
        assertEquals(ScrollAction.Nothing, decideScroll(shell, 0))
        assertEquals(ScrollAction.Nothing, decideScroll(shell.copy(mouseTracking = true), 0))
    }

    // ---- arrow bytes -----------------------------------------------------

    @Test
    fun normalArrow_usesCSI() {
        assertArrayEquals("\u001b[A".toByteArray(), arrowBytes(-1, cursorKeysApplication = false))
        assertArrayEquals("\u001b[B".toByteArray(), arrowBytes(1, cursorKeysApplication = false))
    }

    /** DECCKM on: SS3, not CSI. The wrong form does not scroll — it becomes garbage. */
    @Test
    fun applicationModeArrow_usesSS3() {
        assertArrayEquals("\u001bOA".toByteArray(), arrowBytes(-1, cursorKeysApplication = true))
        assertArrayEquals("\u001bOB".toByteArray(), arrowBytes(1, cursorKeysApplication = true))
    }

    @Test
    fun arrowRepeatsOncePerRow() {
        assertArrayEquals(
            "\u001b[A\u001b[A\u001b[A".toByteArray(),
            arrowBytes(-3, cursorKeysApplication = false),
        )
    }

    @Test
    fun zeroRowArrow_producesNoBytes() {
        assertEquals(0, arrowBytes(0, cursorKeysApplication = false).size)
    }
}
