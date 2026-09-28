package dev.servercontrolpanel.feature.terminal.selection

import android.view.Menu
import android.view.ViewGroup
import androidx.activity.ComponentActivity
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import dev.servercontrolpanel.feature.terminal.input.TerminalInputView
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class TerminalActionModeTest {

    @get:Rule
    val composeTestRule = createAndroidComposeRule<ComponentActivity>()

    private fun withBar(
        tile: (bar: TerminalActionMode, recorder: Recorder) -> Unit,
    ) {
        val recorder = Recorder()
        lateinit var bar: TerminalActionMode
        composeTestRule.runOnUiThread {
            val host = TerminalInputView(composeTestRule.activity)
            val root = composeTestRule.activity.findViewById<ViewGroup>(android.R.id.content)
            root.addView(host)
            host.requestFocus()
            bar = TerminalActionMode(
                host = host,
                onCopy = { recorder.copied++ },
                onSelectAll = { recorder.selectedAll++ },
                onPaste = { recorder.pasted++ },
                onShare = { recorder.shared++ },
                onSendToTerminal = { recorder.sentCount++ },
                onClose = { recorder.closed++ },
                selectionRect = { Rect(0f, 0f, 120f, 40f) },
            )
        }
        composeTestRule.waitForIdle()
        tile(bar, recorder)
    }

    private class Recorder {
        var copied = 0
        var selectedAll = 0
        var pasted = 0
        var shared = 0
        var sentCount = 0
        var closed = 0
    }

    @Test
    fun systemGrantsFloatingActionModeToTerminalGrid() = withBar { bar, _ ->
        composeTestRule.runOnUiThread { bar.show() }
        composeTestRule.waitForIdle()

        assertTrue(
            "without an ActionMode granted by the system there is no floating bar, and the failure would be silent",
            bar.isShowing(),
        )
    }

    @Test
    fun showTwice_doesNotOpenSecondBar() = withBar { bar, _ ->
        composeTestRule.runOnUiThread {
            bar.show()
            bar.show()
            bar.update()
        }
        composeTestRule.waitForIdle()

        assertTrue(bar.isShowing())
    }

    @Test
    fun hideFromInside_doesNotNotifyCloserBack() = withBar { bar, recorder ->
        composeTestRule.runOnUiThread {
            bar.show()
            bar.hide()
        }
        composeTestRule.waitForIdle()

        assertFalse(bar.isShowing())
        assertEquals("closing from inside must not echo back", 0, recorder.closed)
    }

    @Test
    fun hideWithoutShownBar_doesNothing() = withBar { bar, recorder ->
        composeTestRule.runOnUiThread { bar.hide() }
        composeTestRule.waitForIdle()

        assertFalse(bar.isShowing())
        assertEquals(0, recorder.closed)
    }

    @Test
    fun systemMenuGetsAllActions_withDeviceLabels() = withBar { bar, _ ->
        composeTestRule.runOnUiThread { bar.show() }
        composeTestRule.waitForIdle()

        val menu = checkNotNull(bar.menuShown()) { "no menu means no bar" }
        assertEquals(barItems().size, menu.size())
        for (item in barItems()) {
            val entry = checkNotNull(menu.findItem(item.id)) { "missing ${item.action} in the real menu" }
            val expected = item.systemTitle
                ?.let { composeTestRule.activity.getString(it) }
                ?: item.customTitle
            assertEquals("label of ${item.action}", expected, entry.title.toString())
            assertEquals("order of ${item.action}", item.order, entry.order)
            assertTrue("${item.action} must be visible", entry.isVisible)
            assertTrue("${item.action} must be enabled", entry.isEnabled)
        }
    }

    @Test
    fun pasteIsInMenuEvenWithNothingCopied() = withBar { bar, _ ->
        composeTestRule.runOnUiThread { bar.show() }
        composeTestRule.waitForIdle()

        val paste = checkNotNull(bar.menuShown()).findItem(Menu.FIRST + SelectionAction.PASTE.ordinal)
        assertTrue("Paste must not depend on the clipboard contents", paste != null)
    }

    @Test
    fun clickingEachItem_dispatchesMatchingAction() = withBar { bar, recorder ->
        composeTestRule.runOnUiThread { bar.show() }
        composeTestRule.waitForIdle()

        trigger(bar, SelectionAction.SELECT_ALL)
        assertEquals(1, recorder.selectedAll)
        assertTrue("select all changes the selection without ending the gesture", bar.isShowing())

        trigger(bar, SelectionAction.COPY)
        assertEquals(1, recorder.copied)
        assertFalse("copy ends the selection", bar.isShowing())

        reopenAndTrigger(bar, SelectionAction.PASTE)
        assertEquals(1, recorder.pasted)

        reopenAndTrigger(bar, SelectionAction.SHARE)
        assertEquals(1, recorder.shared)

        reopenAndTrigger(bar, SelectionAction.SEND_TO_TERMINAL)
        assertEquals(1, recorder.sentCount)
    }

    private fun trigger(bar: TerminalActionMode, action: SelectionAction) {
        composeTestRule.runOnUiThread {
            checkNotNull(bar.menuShown())
                .performIdentifierAction(Menu.FIRST + action.ordinal, 0)
        }
        composeTestRule.waitForIdle()
    }

    private fun reopenAndTrigger(bar: TerminalActionMode, action: SelectionAction) {
        composeTestRule.runOnUiThread { bar.show() }
        composeTestRule.waitForIdle()
        trigger(bar, action)
    }
}
