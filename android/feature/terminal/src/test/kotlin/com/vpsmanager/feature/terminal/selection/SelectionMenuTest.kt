package com.vpsmanager.feature.terminal.selection

import android.content.Intent
import android.view.Menu
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Which floating selection bar actions stay visible and which go to overflow.
 * [barItems] is pure data (system labels are inlined constants); Robolectric is only
 * needed for the `Intent` tests. How Android actually lays out the menu is covered by
 * `TerminalActionModeTest` on a device.
 */
@RunWith(RobolectricTestRunner::class)
class SelectionMenuTest {

    private fun item(action: SelectionAction) = barItems().single { it.action == action }

    @Test
    fun copyAndPasteAreTheTwoBarActions() {
        val inBar = barItems().filter { it.highlight == Placement.BAR }.map { it.action }

        assertEquals(
            "copy and paste always show, everything else goes to overflow",
            listOf(SelectionAction.COPY, SelectionAction.PASTE),
            inBar,
        )
    }

    @Test
    fun everythingElse_goesToOverflowMenu() {
        val inOverflow = barItems().filter { it.highlight == Placement.OVERFLOW }.map { it.action }

        assertEquals(
            // AOSP order (Share = 7 before Select all = 8), then our own item last.
            listOf(
                SelectionAction.SHARE,
                SelectionAction.SELECT_ALL,
                SelectionAction.SEND_TO_TERMINAL,
            ),
            inOverflow,
        )
    }

    @Test
    fun itemOrderMatchesAndroidTextField() {
        // AOSP's `Editor.ACTION_MODE_MENU_ITEM_ORDER_*`, so the order matches other
        // text fields on the device.
        assertEquals(5, SelectionAction.COPY.order)
        assertEquals(6, SelectionAction.PASTE.order)
        assertEquals(7, SelectionAction.SHARE.order)
        assertEquals(8, SelectionAction.SELECT_ALL.order)
        // Our own items come after AOSP's last item (11).
        assertTrue(SelectionAction.SEND_TO_TERMINAL.order > 11)
        // Other apps' items come after all of ours.
        assertTrue(barItems().all { it.order < OTHER_APPS_ORDER })
    }

    @Test
    fun barDoesNotGrowUnlessSomeoneDecides() {
        // Too many items fill the main panel and make overflow unpredictable; changing
        // the set should force a review of the hierarchy.
        assertEquals(2, barItems().count { it.highlight == Placement.BAR })
        assertEquals(5, barItems().size)
    }

    @Test
    fun copyIsFirstItem_becauseItIsMostUsed() {
        // `layoutMainPanelItems` always promotes the first item to the main panel,
        // so it must be Copy rather than a secondary item.
        assertEquals(SelectionAction.COPY, barItems().first().action)
    }

    @Test
    fun declaredOrderIsMenuOrder() {
        // `FloatingToolbar.mMenuItemComparator` sorts by `getOrder()`, so orders must be
        // strictly increasing.
        val orders = barItems().map { it.order }

        assertEquals(orders.sorted(), orders)
        assertEquals("no repeated orders", orders.size, orders.toSet().size)
    }

    @Test
    fun eachItemHasOwnStableId() {
        val ids = barItems().map { it.id }

        assertEquals("a repeated id would make a click fire the wrong action", ids.size, ids.toSet().size)
        assertTrue("menu ids start at Menu.FIRST", ids.all { it >= Menu.FIRST })
    }

    @Test
    fun idBackToAction_forClickDispatch() {
        for (expected in SelectionAction.entries) {
            assertEquals(expected, actionForItem(item(expected).id))
        }
    }

    @Test
    fun unknownId_mapsToNoAction() {
        // Ids outside our range return null so the callback returns `false` and the
        // system handles them.
        assertNull(actionForItem(Menu.FIRST - 1))
        assertNull(actionForItem(Menu.FIRST + SelectionAction.entries.size))
        assertNull(actionForItem(0))
    }

    @Test
    fun pasteIsNotConditional_itStaysInBarWithoutAnyQuery() {
        // `barItems()` takes no state, so Paste cannot depend on the clipboard (see
        // the notes on `getVisibleAndEnabledMenuItems` and Android 12's paste notice).
        assertNotNull(item(SelectionAction.PASTE))
        assertEquals(Placement.BAR, item(SelectionAction.PASTE).highlight)
    }

    @Test
    fun systemLabelsWhereTheyExist_oursOnlyWhereTheyDoNot() {
        // System labels are already localized; there is no public
        // `android.R.string.share`, so Share uses our own label.
        assertEquals(android.R.string.copy, item(SelectionAction.COPY).systemTitle)
        assertEquals(android.R.string.paste, item(SelectionAction.PASTE).systemTitle)
        assertEquals(android.R.string.selectAll, item(SelectionAction.SELECT_ALL).systemTitle)

        assertEquals("Share", item(SelectionAction.SHARE).customTitle)
        assertEquals("Send to terminal", item(SelectionAction.SEND_TO_TERMINAL).customTitle)
    }

    @Test
    fun otherAppId_doesNotCollideWithOurs() {
        // Other apps' actions (ACTION_PROCESS_TEXT) use a separate id range so they
        // never trigger our actions.
        val ours = barItems().map { it.id }.toSet()
        val fromOthers = List(20) { OtherAppAction("app $it", "p$it", "c$it").id(it) }

        assertTrue(ours.none { it in fromOthers })
        assertTrue("another app's id never becomes one of our actions", fromOthers.all { actionForItem(it) == null })
    }

    @Test
    fun otherAppIndex_onlyRecognizesWhatIsInMenu() {
        assertEquals(0, otherAppIndex(OTHER_APPS_BASE_ID, count = 2))
        assertEquals(1, otherAppIndex(OTHER_APPS_BASE_ID + 1, count = 2))
        // The app may have been uninstalled since the menu was built; return null
        // instead of throwing.
        assertNull(otherAppIndex(OTHER_APPS_BASE_ID + 2, count = 2))
        assertNull(otherAppIndex(OTHER_APPS_BASE_ID - 1, count = 2))
        assertNull(otherAppIndex(Menu.FIRST, count = 2))
    }

    @Test
    fun processTextIntent_carriesTextAndSaysReadOnly() {
        val action = OtherAppAction("Translate", "com.example.translator", "com.example.translator.Screen")
        val intent = processTextIntent(action, "ls: invalid option -- '2'")

        assertEquals(Intent.ACTION_PROCESS_TEXT, intent.action)
        assertEquals("text/plain", intent.type)
        assertEquals(action.packageName, intent.component?.packageName)
        assertEquals(action.className, intent.component?.className)
        assertEquals("ls: invalid option -- '2'", intent.getCharSequenceExtra(Intent.EXTRA_PROCESS_TEXT))
        // Read-only: the grid shows what the remote program printed.
        assertTrue(intent.getBooleanExtra(Intent.EXTRA_PROCESS_TEXT_READONLY, false))
    }

    @Test
    fun shareIntent_isSystemPlainTextSend() {
        val chooser = shareTextIntent("build error")

        assertEquals(Intent.ACTION_CHOOSER, chooser.action)
        val sent = chooser.getParcelableExtra(Intent.EXTRA_INTENT, Intent::class.java)
        assertEquals(Intent.ACTION_SEND, sent?.action)
        assertEquals("text/plain", sent?.type)
        assertEquals("build error", sent?.getStringExtra(Intent.EXTRA_TEXT))
    }

    @Test
    fun anItemNeverHasBothLabels_norNeither() {
        val error = runCatching {
            BarItem(SelectionAction.COPY, Placement.BAR)
        }.exceptionOrNull()

        assertTrue("an item without a label would show up blank in the bar", error is IllegalArgumentException)
    }
}
