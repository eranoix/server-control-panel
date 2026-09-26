package com.vpsmanager.feature.terminal.scroll

import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performTouchInput
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.vpsmanager.feature.terminal.selection.CanvasTapTarget
import com.vpsmanager.feature.terminal.selection.CellHitTester
import com.vpsmanager.feature.terminal.selection.GridSelectionHolder
import com.vpsmanager.feature.terminal.selection.SelectionGestureController
import com.vpsmanager.feature.terminal.selection.canvasDragGestures
import com.vpsmanager.feature.terminal.selection.canvasTapGesture
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/** A comfortable margin over the device/emulator `longPressTimeoutMillis`. */
private const val LONG_PRESS_SLACK_MS = 700L

/**
 * Arbitration between scroll, tap, long press and drag, driven by real touch through
 * Compose (`pointerInput` does not run on the JVM). The modifier chain matches
 * `TerminalRoute` in the same order, since the innermost modifier sees events first.
 */
@RunWith(AndroidJUnit4::class)
class ScrollGestureArbitrationTest {

    @get:Rule
    val composeTestRule = createAndroidComposeRule<ComponentActivity>()

    private class Spy : CanvasScrollTarget {
        var starts = 0
        var totalPx = 0f
        var ends = 0
        override fun onScrollStart() { starts++ }
        override fun onScroll(deltaPx: Float, position: Offset): Boolean {
            totalPx += deltaPx
            return true
        }
        override fun onScrollEnd() { ends++ }
    }

    private class Grid {
        val scrollSpy = Spy()
        val selection = GridSelectionHolder()
        val taps = mutableListOf<Int>()
        val selectionController = SelectionGestureController(
            { CellHitTester(cellWidthPx = 20f, cellHeightPx = 40f, cols = 40, rows = 40) },
            selection,
        )
    }

    private fun build(grid: Grid) {
        composeTestRule.setContent {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .canvasDragGestures(grid.selectionController)
                    .canvasTapGesture(CanvasTapTarget { _, taps -> grid.taps += taps })
                    .canvasScrollGesture(grid.scrollSpy),
            )
        }
    }

    /** A fast vertical drag scrolls without raising the keyboard or starting a selection. */
    @Test
    fun fastVerticalDrag_scrolls_withoutOpeningKeyboardOrSelecting() {
        val grid = Grid()
        build(grid)

        composeTestRule.onRoot().performTouchInput {
            down(center)
            moveTo(center + Offset(0f, 60f))
            moveTo(center + Offset(0f, 140f))
            moveTo(center + Offset(0f, 220f))
            up()
        }
        composeTestRule.waitForIdle()

        assertEquals("the vertical drag should have been claimed", 1, grid.scrollSpy.starts)
        assertTrue("should have scrolled into the past", grid.scrollSpy.totalPx > 100f)
        assertTrue(
            "a vertical drag must not count as a tap (it would raise the keyboard)",
            grid.taps.isEmpty(),
        )
        assertNull(
            "a vertical drag must not start a selection",
            grid.selection.selection,
        )
    }

    /** Long press and drag still selects; scrolling backs off once the long press wins. */
    @Test
    fun longPressAndDrag_stillSelects_withoutScrolling() {
        val grid = Grid()
        build(grid)

        composeTestRule.onRoot().performTouchInput {
            down(center)
            advanceEventTime(LONG_PRESS_SLACK_MS)
        }
        // The long-press timer runs on the test's virtual clock; `advanceEventTime`
        // only stamps the MotionEvent, so the clock must be advanced too.
        composeTestRule.mainClock.advanceTimeBy(LONG_PRESS_SLACK_MS)

        composeTestRule.onRoot().performTouchInput {
            moveTo(center + Offset(120f, 90f))
            up()
        }
        composeTestRule.waitForIdle()

        assertNotNull(
            "long press with drag should still select",
            grid.selection.selection,
        )
        assertEquals(
            "held past the long press, scrolling should have given up",
            0,
            grid.scrollSpy.starts,
        )
    }

    /** A short tap is still a tap (it raises the keyboard). */
    @Test
    fun shortTap_staysATap_withoutScrolling() {
        val grid = Grid()
        build(grid)

        composeTestRule.onRoot().performTouchInput {
            down(center)
            up()
        }
        composeTestRule.waitForIdle()

        assertEquals("the short tap should have been delivered", listOf(1), grid.taps)
        assertEquals(0, grid.scrollSpy.starts)
        assertNull(grid.selection.selection)
    }

    /** Scrolling does not consume horizontal drags, leaving them to other handlers. */
    @Test
    fun horizontalDrag_isNotClaimedByScroll() {
        val grid = Grid()
        build(grid)

        composeTestRule.onRoot().performTouchInput {
            down(center)
            moveTo(center + Offset(80f, 0f))
            moveTo(center + Offset(200f, 0f))
            up()
        }
        composeTestRule.waitForIdle()

        assertEquals(
            "a horizontal drag must not become vertical scrolling",
            0,
            grid.scrollSpy.starts,
        )
    }

    /** The gesture reports its end, which resets the pixel accumulator. */
    @Test
    fun verticalDrag_endsGestureOnFingerUp() {
        val grid = Grid()
        build(grid)

        composeTestRule.onRoot().performTouchInput {
            down(center)
            moveTo(center + Offset(0f, 80f))
            moveTo(center + Offset(0f, 160f))
            up()
        }
        composeTestRule.waitForIdle()
        // Inertia can prolong the gesture; advance the clock until it ends.
        composeTestRule.mainClock.advanceTimeBy(3_000L)
        composeTestRule.waitForIdle()

        assertEquals(1, grid.scrollSpy.starts)
        assertEquals("every claimed gesture must end", 1, grid.scrollSpy.ends)
    }
}
