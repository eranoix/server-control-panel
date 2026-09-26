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
 * The contest of four gestures over the SAME finger, driven by real touch
 * through the actual Compose pipeline — the one path a JVM test cannot cover,
 * because `pointerInput` does not run there.
 *
 * The grid here assembles exactly the same modifier chain as `TerminalRoute`,
 * in the same order, because the order is part of the contract: the innermost
 * modifier receives the event first on the `Main` pass.
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

    /**
     * The new gesture: a fast vertical drag scrolls — and does **not** raise
     * the keyboard or start a selection. That was the entire risk of adding a
     * fourth gesture to a finger that already had three owners.
     */
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

        assertEquals("o arraste vertical tinha que ser reivindicado", 1, grid.scrollSpy.starts)
        assertTrue("tinha que rolar para o passado", grid.scrollSpy.totalPx > 100f)
        assertTrue(
            "o arraste vertical NÃO pode contar como toque (subiria o teclado)",
            grid.taps.isEmpty(),
        )
        assertNull(
            "o arraste vertical NÃO pode iniciar seleção",
            grid.selection.selection,
        )
    }

    /**
     * The old gesture survives whole: press and drag selects text, and
     * scrolling stays out of it — it bows out as soon as the long press wins.
     */
    @Test
    fun longPressAndDrag_stillSelects_withoutScrolling() {
        val grid = Grid()
        build(grid)

        composeTestRule.onRoot().performTouchInput {
            down(center)
            advanceEventTime(LONG_PRESS_SLACK_MS)
        }
        // The long-press timer runs on the test's VIRTUAL clock; the
        // `advanceEventTime` only stamps the MotionEvent. Without advancing the
        // clock here, the long press never fires (see the twin note in
        // SelectionComposeIndependenceTest).
        composeTestRule.mainClock.advanceTimeBy(LONG_PRESS_SLACK_MS)

        composeTestRule.onRoot().performTouchInput {
            moveTo(center + Offset(120f, 90f))
            up()
        }
        composeTestRule.waitForIdle()

        assertNotNull(
            "o toque longo com arraste tinha que continuar selecionando",
            grid.selection.selection,
        )
        assertEquals(
            "parado além do toque longo, a rolagem tinha que ter desistido",
            0,
            grid.scrollSpy.starts,
        )
    }

    /** A short tap is still a tap — it is what raises the keyboard. */
    @Test
    fun shortTap_staysATap_withoutScrolling() {
        val grid = Grid()
        build(grid)

        composeTestRule.onRoot().performTouchInput {
            down(center)
            up()
        }
        composeTestRule.waitForIdle()

        assertEquals("o toque curto tinha que ser entregue", listOf(1), grid.taps)
        assertEquals(0, grid.scrollSpy.starts)
        assertNull(grid.selection.selection)
    }

    /**
     * A horizontal drag is not ours. Scrolling leaves the field without
     * consuming anything, so as not to steal a gesture from whoever may come
     * to want it.
     */
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
            "arraste horizontal não pode virar rolagem vertical",
            0,
            grid.scrollSpy.starts,
        )
    }

    /** The gesture announces it is over — that is what resets the pixel accumulator. */
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
        assertEquals("todo gesto reivindicado tem que terminar", 1, grid.scrollSpy.ends)
    }
}
