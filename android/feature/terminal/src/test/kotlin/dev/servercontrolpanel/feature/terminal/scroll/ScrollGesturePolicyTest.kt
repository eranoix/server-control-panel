package dev.servercontrolpanel.feature.terminal.scroll

import dev.servercontrolpanel.terminalengine.TerminalModes
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Test

class ScrollGesturePolicyTest {

    private val shell = TerminalModes.NONE

    @Test
    fun normalScreenWithoutMouse_scrollsLocalHistory() {
        assertEquals(ScrollAction.Viewport(-3), decideScroll(shell, -3))
        assertEquals(ScrollAction.Viewport(5), decideScroll(shell, 5))
    }

    @Test
    fun programAskedForMouse_becomesWheel_evenOnNormalScreen() {
        val withMouse = shell.copy(mouseTracking = true)
        assertEquals(ScrollAction.Wheel(-3), decideScroll(withMouse, -3))
    }

    @Test
    fun programAskedForMouse_becomesWheel_alsoOnAltScreen() {
        val htop = shell.copy(mouseTracking = true, altScreen = true, altScroll = true)
        assertEquals(ScrollAction.Wheel(-2), decideScroll(htop, -2))
    }

    @Test
    fun altScreenWithAltScroll_becomesArrow() {
        val less = shell.copy(altScreen = true, altScroll = true)
        assertEquals(ScrollAction.Arrows(-4), decideScroll(less, -4))
    }

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

    @Test
    fun normalArrow_usesCSI() {
        assertArrayEquals("\u001b[A".toByteArray(), arrowBytes(-1, cursorKeysApplication = false))
        assertArrayEquals("\u001b[B".toByteArray(), arrowBytes(1, cursorKeysApplication = false))
    }

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
