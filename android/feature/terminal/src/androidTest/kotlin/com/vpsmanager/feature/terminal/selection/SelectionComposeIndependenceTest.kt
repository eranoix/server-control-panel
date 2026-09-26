package com.vpsmanager.feature.terminal.selection

import android.view.inputmethod.EditorInfo
import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performTouchInput
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.vpsmanager.feature.terminal.input.TerminalInputConnection
import com.vpsmanager.feature.terminal.input.TerminalInputView
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * A comfortable margin over `ViewConfiguration`'s `longPressTimeoutMillis` (400–500 ms on
 * devices and on the emulator), used both for the event time and for the test's virtual clock.
 */
private const val LONG_PRESS_SLACK_MS = 700L

/**
 * Drives a real Compose touch gesture through the actual
 * `pointerInput`/`detectDragGesturesAfterLongPress` pipeline
 * ([canvasDragGestures], [SelectionGestureController]) side by side with a
 * real [TerminalInputConnection] obtained the same way the IME obtains one
 * ([TerminalInputView.onCreateInputConnection]), and asserts neither ever
 * observes the other's state change. [TerminalInputConnectionTest]'s
 * `` `grid selection and ime composition state do not observe each other` ``
 * proves this at the direct-API-call level; this test extends the same
 * independence property to a genuine touch gesture dispatched through
 * Compose's real gesture-detection pipeline instead of a direct
 * `controller.onDrag(...)` call, which is the one path that unit tests
 * (JVM/Robolectric, no real `pointerInput` coroutine dispatch) cannot cover.
 *
 * This requires a connected device/emulator to actually run
 * (`connectedDebugAndroidTest`) — none is available in this environment.
 * Only compilation against the real APIs is verified here.
 */
@RunWith(AndroidJUnit4::class)
class SelectionComposeIndependenceTest {

    @get:Rule
    val composeTestRule = createAndroidComposeRule<ComponentActivity>()

    @Test
    fun realTouchDragSelection_andImeComposition_neverObserveEachOther() {
        val selectionHolder = GridSelectionHolder()
        val hitTester = CellHitTester(cellWidthPx = 20f, cellHeightPx = 40f, cols = 20, rows = 20)
        val controller = SelectionGestureController({ hitTester }, selectionHolder)

        composeTestRule.setContent {
            Box(modifier = Modifier.fillMaxSize().canvasDragGestures(controller))
        }

        lateinit var connection: TerminalInputConnection
        composeTestRule.runOnUiThread {
            val inputView = TerminalInputView(composeTestRule.activity)
            connection = inputView.onCreateInputConnection(EditorInfo()) as TerminalInputConnection
        }

        // Direction 1: IME composition is already in flight before any touch
        // gesture starts.
        composeTestRule.runOnUiThread {
            connection.setComposingText("nih", 1)
        }
        assertNull(
            "no drag has happened yet, the grid selection must still be null",
            selectionHolder.selection,
        )

        // Real long-press-then-drag, dispatched through the actual
        // pointerInput/detectDragGesturesAfterLongPress pipeline (not a
        // direct SelectionGestureController.onDrag(...) call). Split across
        // two performTouchInput blocks around the composing mutation below
        // so the drag is still in flight while composition changes.
        composeTestRule.onRoot().performTouchInput {
            down(center)
            advanceEventTime(LONG_PRESS_SLACK_MS)
        }
        // `advanceEventTime` only stamps the timestamp of the injected MotionEvents;
        // the long-press timer of `detectDragGesturesAfterLongPress` runs as a `delay`
        // on the test's VIRTUAL clock, and the `waitForIdle` implicit at the end of a
        // `performTouchInput` block does not advance it. With the gesture split across
        // two blocks (necessary here: the IME composition mutation happens in the
        // middle of it), the long press never fired and no selection was created —
        // measured on the emulator: without this line the selection stays null, with it
        // the START is anchored. This is not assertion slack: it advances the very
        // clock the detector itself observes.
        composeTestRule.mainClock.advanceTimeBy(LONG_PRESS_SLACK_MS)
        assertNotNull(
            "the long press must have anchored a selection before the drag moves",
            selectionHolder.selection,
        )

        // Mutate composing state mid-gesture: the long-press has fired
        // (anchoring a selection start cell) but the drag has not moved or
        // ended yet.
        composeTestRule.runOnUiThread {
            connection.setComposingText("niha", 1)
        }
        assertEquals(
            "an in-flight touch drag must not disturb IME composition",
            "niha",
            connection.composingTextForTest(),
        )

        composeTestRule.onRoot().performTouchInput {
            moveTo(center + Offset(100f, 80f))
            up()
        }
        composeTestRule.waitForIdle()

        assertEquals(
            "the completed drag must not have touched IME composition either",
            "niha",
            connection.composingTextForTest(),
        )
        val selectionAfterDrag = selectionHolder.selection
        assertNotNull(
            "a real long-press-drag through the actual gesture pipeline must produce a selection",
            selectionAfterDrag,
        )

        // Direction 2: a selection now exists; committing/finishing the IME
        // composition afterwards must not touch it.
        composeTestRule.runOnUiThread {
            connection.commitText("好", 1)
            connection.finishComposingText()
        }
        assertEquals(
            "committing/finishing composition after a drag must not touch the existing selection",
            selectionAfterDrag,
            selectionHolder.selection,
        )
    }
}
