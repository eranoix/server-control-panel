package dev.servercontrolpanel.feature.terminal.selection

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
import dev.servercontrolpanel.feature.terminal.input.TerminalInputConnection
import dev.servercontrolpanel.feature.terminal.input.TerminalInputView
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

private const val LONG_PRESS_SLACK_MS = 700L

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

        composeTestRule.runOnUiThread {
            connection.setComposingText("nih", 1)
        }
        assertNull(
            "no drag has happened yet, the grid selection must still be null",
            selectionHolder.selection,
        )

        composeTestRule.onRoot().performTouchInput {
            down(center)
            advanceEventTime(LONG_PRESS_SLACK_MS)
        }
        composeTestRule.mainClock.advanceTimeBy(LONG_PRESS_SLACK_MS)
        assertNotNull(
            "the long press must have anchored a selection before the drag moves",
            selectionHolder.selection,
        )

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
