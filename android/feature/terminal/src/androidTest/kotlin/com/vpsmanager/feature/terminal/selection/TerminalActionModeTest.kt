package com.vpsmanager.feature.terminal.selection

import android.view.Menu
import android.view.ViewGroup
import androidx.activity.ComponentActivity
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.vpsmanager.feature.terminal.input.TerminalInputView
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * The SYSTEM's floating bar over the terminal.
 *
 * What this test proves, and no JVM test could, is that Android really does
 * **grant** a floating-type `ActionMode` to this `View`. A `startActionMode`
 * may return `null` — if the view is not attached, if the window does not
 * support it, if the theme has no action mode — and in that case the bar
 * would simply not appear, silently. Only by running on a device can you
 * know.
 *
 * Before this there were "Copiar"/"Colar" buttons drawn by the app at the
 * foot of the grid. This is the object that replaced them.
 */
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
            // Attach it for real: a loose view never gets an `ActionMode`.
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
            "sem um ActionMode concedido pelo sistema não existe barra flutuante nenhuma — e a falha seria silenciosa",
            bar.isShowing(),
        )
    }

    @Test
    fun showTwice_doesNotOpenSecondBar() = withBar { bar, _ ->
        composeTestRule.runOnUiThread {
            bar.show()
            // The selection changed size: re-anchor, do not stack another bar.
            bar.show()
            bar.update()
        }
        composeTestRule.waitForIdle()

        assertTrue(bar.isShowing())
    }

    @Test
    fun hideFromInside_doesNotNotifyCloserBack() = withBar { bar, recorder ->
        // This is the anti-recursion latch: whoever hides the bar is usually
        // whoever cleared the selection, and telling them back would call
        // `esconder()` again, without end.
        composeTestRule.runOnUiThread {
            bar.show()
            bar.hide()
        }
        composeTestRule.waitForIdle()

        assertFalse(bar.isShowing())
        assertEquals("fechar por dentro não pode ecoar de volta", 0, recorder.closed)
    }

    @Test
    fun hideWithoutShownBar_doesNothing() = withBar { bar, recorder ->
        composeTestRule.runOnUiThread { bar.hide() }
        composeTestRule.waitForIdle()

        assertFalse(bar.isShowing())
        assertEquals(0, recorder.closed)
    }

    // ---- The REAL menu the system assembled ----
    //
    // `SelectionMenuTest` proves the hierarchy over the list of items, on the
    // JVM. These prove the next step, which only exists on the device: that
    // the items arrived intact at the `Menu` of the `ActionMode` granted by
    // the system, with the labels Android itself translates, and that the
    // click dispatches through the right action.

    @Test
    fun systemMenuGetsAllActions_withDeviceLabels() = withBar { bar, _ ->
        composeTestRule.runOnUiThread { bar.show() }
        composeTestRule.waitForIdle()

        val menu = checkNotNull(bar.menuShown()) { "sem menu não há barra" }
        assertEquals(barItems().size, menu.size())
        for (item in barItems()) {
            val entry = checkNotNull(menu.findItem(item.id)) { "faltou ${item.action} no menu real" }
            val expected = item.systemTitle
                ?.let { composeTestRule.activity.getString(it) }
                ?: item.customTitle
            assertEquals("rótulo de ${item.action}", expected, entry.title.toString())
            assertEquals("ordem de ${item.action}", item.order, entry.order)
            // The item has to be visible AND enabled: `FloatingToolbar`
            // filters by `isVisible() && isEnabled()`, and a disabled item
            // disappears from the bar instead of going grey.
            assertTrue("${item.action} tem que estar visível", entry.isVisible)
            assertTrue("${item.action} tem que estar habilitada", entry.isEnabled)
        }
    }

    @Test
    fun pasteIsInMenuEvenWithNothingCopied() = withBar { bar, _ ->
        // The emulator's clipboard starts out empty in this test, and that is
        // exactly the case that used to make the item disappear. Now it stays —
        // the "there is nothing copied" is said on the click, by `PasteAction`.
        composeTestRule.runOnUiThread { bar.show() }
        composeTestRule.waitForIdle()

        val paste = checkNotNull(bar.menuShown()).findItem(Menu.FIRST + SelectionAction.PASTE.ordinal)
        assertTrue("Colar não pode depender do que há na área de transferência", paste != null)
    }

    @Test
    fun clickingEachItem_dispatchesMatchingAction() = withBar { bar, recorder ->
        composeTestRule.runOnUiThread { bar.show() }
        composeTestRule.waitForIdle()

        // "Selecionar tudo" is the only one that does NOT close the bar, so it
        // goes first: the others end the mode and the menu stops existing.
        trigger(bar, SelectionAction.SELECT_ALL)
        assertEquals(1, recorder.selectedAll)
        assertTrue("selecionar tudo troca a seleção, não encerra o gesto", bar.isShowing())

        trigger(bar, SelectionAction.COPY)
        assertEquals(1, recorder.copied)
        assertFalse("copiar encerra a seleção", bar.isShowing())

        reopenAndTrigger(bar, SelectionAction.PASTE)
        assertEquals(1, recorder.pasted)

        reopenAndTrigger(bar, SelectionAction.SHARE)
        assertEquals(1, recorder.shared)

        reopenAndTrigger(bar, SelectionAction.SEND_TO_TERMINAL)
        assertEquals(1, recorder.sentCount)
    }

    private fun trigger(bar: TerminalActionMode, action: SelectionAction) {
        composeTestRule.runOnUiThread {
            // The real path: the same one a tap on the button goes through,
            // Menu -> ActionMode.Callback.onActionItemClicked.
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
