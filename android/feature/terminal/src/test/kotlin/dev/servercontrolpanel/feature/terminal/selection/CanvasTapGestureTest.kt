package dev.servercontrolpanel.feature.terminal.selection

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performTouchInput
import dev.servercontrolpanel.feature.terminal.input.ByteSink
import dev.servercontrolpanel.feature.terminal.mouse.MouseEventEncoder
import dev.servercontrolpanel.feature.terminal.mouse.MouseReportGestureController
import dev.servercontrolpanel.feature.terminal.mouse.TouchRouting
import dev.servercontrolpanel.terminalengine.MouseAction
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/** A comfortable margin over `longPressTimeoutMillis` (400 to 500 ms). */
private const val LONG_PRESS_SLACK_MS = 700L

/** Well inside `doubleTapTimeoutMillis` (300 ms on devices and on the emulator). */
private const val DOUBLE_TAP_INTERVAL_MS = 60L

/**
 * Every meaning a single finger can have on the grid: a short tap opens the keyboard,
 * long press selects ([canvasDragGestures] + [SelectionGestureController]), and
 * double/triple taps count up. Both recognizers are mounted together on the same
 * `Modifier` so the tests prove they coexist.
 */
@RunWith(RobolectricTestRunner::class)
class CanvasTapGestureTest {

    @get:Rule
    val composeRule = createComposeRule()

    private val hitTester = CellHitTester(cellWidthPx = 20f, cellHeightPx = 40f, cols = 40, rows = 40)
    private val selectionHolder = GridSelectionHolder()
    private val selectionController = SelectionGestureController({ hitTester }, selectionHolder)
    private val recordedTaps = mutableListOf<Pair<Offset, Int>>()

    private class RecordingSink : ByteSink {
        val sent = mutableListOf<String>()
        override fun send(bytes: ByteArray) {
            sent += String(bytes, Charsets.US_ASCII)
        }
    }

    private val sink = RecordingSink()

    /** Behaves like the native encoder with mouse tracking active. */
    private val trackingEncoder = MouseEventEncoder { action, _, _, _ ->
        val terminator = if (action == MouseAction.RELEASE) 'm' else 'M'
        "\u001b[<0;1;1$terminator".toByteArray(Charsets.US_ASCII)
    }

    /** The same gesture stack [dev.servercontrolpanel.feature.terminal.ui.TerminalRoute] mounts on the grid. */
    private fun buildGrid(policy: TouchRouting) {
        val mouseController = MouseReportGestureController(trackingEncoder, sink)
        val dragTarget = routeCanvasDrag(policy, selectionController, mouseController)
        val tapTarget = routeCanvasTap(
            policy,
            keyboardTarget = { position, taps ->
                if (taps < DOUBLE_TAP) selectionController.clearSelection()
                recordedTaps += position to taps
            },
            mouseTarget = mouseController,
        )
        composeRule.setContent {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .canvasDragGestures(dragTarget)
                    .canvasTapGesture(tapTarget),
            )
        }
    }

    private fun noMouse() = TouchRouting { false }

    private fun withMouse() = TouchRouting { true }

    @Test
    fun `a short tap on the grid requests the keyboard`() {
        buildGrid(noMouse())

        composeRule.onRoot().performTouchInput { down(center); up() }
        composeRule.waitForIdle()

        assertEquals("a short tap on the grid must request the keyboard once", 1, recordedTaps.size)
        assertEquals("and count as a single tap", SINGLE_TAP, recordedTaps[0].second)
        assertNull("a short tap is not a selection", selectionHolder.selection)
    }

    @Test
    fun `a long press still selects and does not request the keyboard`() {
        buildGrid(noMouse())

        // `advanceEventTime` only stamps event times; the long-press timer runs on the
        // test clock, which must be advanced separately.
        composeRule.onRoot().performTouchInput {
            down(center)
            advanceEventTime(LONG_PRESS_SLACK_MS)
        }
        composeRule.mainClock.advanceTimeBy(LONG_PRESS_SLACK_MS)

        assertNotNull("the long press must anchor the selection", selectionHolder.selection)
        assertTrue("a long press is not a short tap, so no keyboard", recordedTaps.isEmpty())

        composeRule.onRoot().performTouchInput {
            moveTo(center + Offset(120f, 80f))
            up()
        }
        composeRule.waitForIdle()

        val selection = selectionHolder.selection
        assertNotNull("dragging after the long press must keep the selection alive", selection)
        assertTrue(
            "lifting the finger after a selection drag must not become a short tap",
            recordedTaps.isEmpty(),
        )
    }

    @Test
    fun `two quick taps in the same place are a double tap`() {
        buildGrid(noMouse())

        composeRule.onRoot().performTouchInput { down(center); up() }
        composeRule.onRoot().performTouchInput {
            advanceEventTime(DOUBLE_TAP_INTERVAL_MS)
            down(center)
            up()
        }
        composeRule.waitForIdle()

        assertEquals("both taps arrive in order", listOf(SINGLE_TAP, DOUBLE_TAP), recordedTaps.map { it.second })
    }

    @Test
    fun `three quick taps reach the line count and go no further`() {
        buildGrid(noMouse())

        repeat(4) {
            composeRule.onRoot().performTouchInput {
                advanceEventTime(DOUBLE_TAP_INTERVAL_MS)
                down(center)
                up()
            }
        }
        composeRule.waitForIdle()

        assertEquals(
            "there is no gesture above three, so the counter saturates",
            listOf(SINGLE_TAP, DOUBLE_TAP, TRIPLE_TAP, TRIPLE_TAP),
            recordedTaps.map { it.second },
        )
    }

    @Test
    fun `two taps far apart are two single taps`() {
        buildGrid(noMouse())

        composeRule.onRoot().performTouchInput { down(center); up() }
        composeRule.onRoot().performTouchInput {
            advanceEventTime(DOUBLE_TAP_INTERVAL_MS)
            down(center + Offset(150f, 120f))
            up()
        }
        composeRule.waitForIdle()

        assertEquals(
            "tapping opposite corners is not a word gesture",
            listOf(SINGLE_TAP, SINGLE_TAP),
            recordedTaps.map { it.second },
        )
    }

    @Test
    fun `a long press in between breaks the tap sequence`() {
        buildGrid(noMouse())

        composeRule.onRoot().performTouchInput { down(center); up() }
        composeRule.onRoot().performTouchInput {
            down(center)
            advanceEventTime(LONG_PRESS_SLACK_MS)
        }
        composeRule.mainClock.advanceTimeBy(LONG_PRESS_SLACK_MS)
        composeRule.onRoot().performTouchInput { up() }
        composeRule.onRoot().performTouchInput {
            advanceEventTime(DOUBLE_TAP_INTERVAL_MS)
            down(center)
            up()
        }
        composeRule.waitForIdle()

        assertEquals(
            "after a selection drag the next tap starts again from 1",
            listOf(SINGLE_TAP, SINGLE_TAP),
            recordedTaps.map { it.second },
        )
    }

    @Test
    fun `with the program asking for the mouse a tap becomes a click for it, not the keyboard`() {
        buildGrid(withMouse())

        composeRule.onRoot().performTouchInput { down(center); up() }
        composeRule.waitForIdle()

        assertTrue("with the mouse active the tap belongs to the remote program", recordedTaps.isEmpty())
        assertEquals(
            "a click is a press and release pair on the same cell",
            2,
            sink.sent.size,
        )
        assertTrue("the first event is the press (terminator M)", sink.sent[0].endsWith("M"))
        assertTrue("the second is the release (terminator m)", sink.sent[1].endsWith("m"))
    }

    @Test
    fun `with no program asking for the mouse no mouse bytes are emitted`() {
        // At a bash prompt, touches must not put any mouse bytes into the stream (they
        // would show up as garbage text).
        buildGrid(noMouse())

        composeRule.onRoot().performTouchInput { down(center); up() }
        composeRule.onRoot().performTouchInput {
            down(center)
            advanceEventTime(LONG_PRESS_SLACK_MS)
        }
        composeRule.mainClock.advanceTimeBy(LONG_PRESS_SLACK_MS)
        composeRule.onRoot().performTouchInput {
            moveTo(center + Offset(120f, 80f))
            up()
        }
        composeRule.waitForIdle()

        assertTrue(
            "neither tap nor drag may become a mouse sequence when nobody asked for the mouse",
            sink.sent.isEmpty(),
        )
    }
}
