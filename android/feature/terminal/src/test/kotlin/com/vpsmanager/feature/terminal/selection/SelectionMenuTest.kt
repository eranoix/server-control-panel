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
 * The HIERARCHY of the floating selection bar — what stays in sight and what
 * goes behind the three dots.
 *
 * Runs on the JVM, with no emulator. [barItems] is pure data — the
 * system's labels (`android.R.string.*`) are `static final int` with a
 * `ConstantValue` in `android.jar`, so the compiler inlines them as literals
 * and nothing consults Android at runtime. Robolectric only enters through the
 * `Intent` tests, which need the real implementation (`putExtra`,
 * `createChooser`) rather than the stub that throws.
 *
 * What this test does NOT prove is the other side of the contract: that
 * Android really does translate `SHOW_AS_ACTION_ALWAYS`/`NEVER` into
 * bar/overflow. That is framework behaviour, documented in [Placement] from the
 * AOSP source, and the proof is `TerminalActionModeTest` (a real menu, on a
 * device) plus the emulator screenshot.
 */
@RunWith(RobolectricTestRunner::class)
class SelectionMenuTest {

    private fun item(action: SelectionAction) = barItems().single { it.action == action }

    @Test
    fun copyAndPasteAreTheTwoBarActions() {
        val inBar = barItems().filter { it.highlight == Placement.BAR }.map { it.action }

        assertEquals(
            "o pedido é literal: copiar e colar sempre aparecem, o resto vai pro estouro",
            listOf(SelectionAction.COPY, SelectionAction.PASTE),
            inBar,
        )
    }

    @Test
    fun everythingElse_goesToOverflowMenu() {
        val inOverflow = barItems().filter { it.highlight == Placement.OVERFLOW }.map { it.action }

        assertEquals(
            // Share before Select all is AOSP's own order (`_SHARE = 7`
            // comes before `_SELECT_ALL = 8`); the item that is only ours goes
            // last.
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
        // The numbers are AOSP's `Editor.ACTION_MODE_MENU_ITEM_ORDER_*`:
        // COPY=5, PASTE=6, SHARE=7, SELECT_ALL=8. Reusing them is what makes
        // the terminal's bar present its actions in the same sequence as any
        // other field on the device.
        assertEquals(5, SelectionAction.COPY.order)
        assertEquals(6, SelectionAction.PASTE.order)
        assertEquals(7, SelectionAction.SHARE.order)
        assertEquals(8, SelectionAction.SELECT_ALL.order)
        // What is only ours comes after AOSP's last item (=11).
        assertTrue(SelectionAction.SEND_TO_TERMINAL.order > 11)
        // And anything from an outside app comes after everything of ours.
        assertTrue(barItems().all { it.order < OTHER_APPS_ORDER })
    }

    @Test
    fun barDoesNotGrowUnlessSomeoneDecides() {
        // A crowded bar is as bad as an empty one: with too many items the
        // main panel fills up by width and the `⋮` stops being predictable.
        // This test is the gate — changing the set forces a review of the
        // hierarchy along with it.
        assertEquals(2, barItems().count { it.highlight == Placement.BAR })
        assertEquals(5, barItems().size)
    }

    @Test
    fun copyIsFirstItem_becauseItIsMostUsed() {
        // It is also the safety net for `layoutMainPanelItems`: it promotes
        // the FIRST item to the main panel even if that item asked for
        // overflow. With Copy at the front, that exception never puts a
        // secondary item on the bar by accident.
        assertEquals(SelectionAction.COPY, barItems().first().action)
    }

    @Test
    fun declaredOrderIsMenuOrder() {
        // `FloatingToolbar.mMenuItemComparator` breaks ties by `getOrder()`
        // within each showAsAction class — if the order is not strictly
        // increasing, the bar shuffles itself.
        val orders = barItems().map { it.order }

        assertEquals(orders.sorted(), orders)
        assertEquals("sem ordens repetidas", orders.size, orders.toSet().size)
    }

    @Test
    fun eachItemHasOwnStableId() {
        val ids = barItems().map { it.id }

        assertEquals("id repetido faria um clique acionar a ação errada", ids.size, ids.toSet().size)
        assertTrue("ids do menu começam em Menu.FIRST", ids.all { it >= Menu.FIRST })
    }

    @Test
    fun idBackToAction_forClickDispatch() {
        for (expected in SelectionAction.entries) {
            assertEquals(expected, actionForItem(item(expected).id))
        }
    }

    @Test
    fun unknownId_mapsToNoAction() {
        // The bar may host items that are not ours; an id outside the range
        // has to return null so the callback answers `false` and lets the
        // system handle it, rather than firing the last action in the list.
        assertNull(actionForItem(Menu.FIRST - 1))
        assertNull(actionForItem(Menu.FIRST + SelectionAction.entries.size))
        assertNull(actionForItem(0))
    }

    @Test
    fun pasteIsNotConditional_itStaysInBarWithoutAnyQuery() {
        // The test that protects the decision: `barItems()` takes no
        // state at all. If someone one day wants to hide "Paste" with an empty
        // clipboard, they will have to change the signature — and then they
        // will read the comment about `getVisibleAndEnabledMenuItems` and
        // about Android 12's notice.
        assertNotNull(item(SelectionAction.PASTE))
        assertEquals(Placement.BAR, item(SelectionAction.PASTE).highlight)
    }

    @Test
    fun systemLabelsWhereTheyExist_oursOnlyWhereTheyDoNot() {
        // Copy/Paste/Select all come from Android itself, already translated
        // into the device's language. Share has no public
        // `android.R.string.share` (checked in the SDK 37 android.jar), so it
        // is ours.
        assertEquals(android.R.string.copy, item(SelectionAction.COPY).systemTitle)
        assertEquals(android.R.string.paste, item(SelectionAction.PASTE).systemTitle)
        assertEquals(android.R.string.selectAll, item(SelectionAction.SELECT_ALL).systemTitle)

        assertEquals("Share", item(SelectionAction.SHARE).customTitle)
        assertEquals("Send to terminal", item(SelectionAction.SEND_TO_TERMINAL).customTitle)
    }

    // ---- Other apps' actions (ACTION_PROCESS_TEXT) ----

    @Test
    fun otherAppId_doesNotCollideWithOurs() {
        // The defect this separate range avoids: "Translate" firing "Send to
        // the terminal" because the ids ran into each other.
        val ours = barItems().map { it.id }.toSet()
        val fromOthers = List(20) { OtherAppAction("app $it", "p$it", "c$it").id(it) }

        assertTrue(ours.none { it in fromOthers })
        assertTrue("id de outro app nunca vira ação nossa", fromOthers.all { actionForItem(it) == null })
    }

    @Test
    fun otherAppIndex_onlyRecognizesWhatIsInMenu() {
        assertEquals(0, otherAppIndex(OTHER_APPS_BASE_ID, count = 2))
        assertEquals(1, otherAppIndex(OTHER_APPS_BASE_ID + 1, count = 2))
        // Outside the assembled range: the app may have been uninstalled
        // between building the menu and the click. `null` makes the callback
        // return false rather than throwing an IndexOutOfBounds.
        assertNull(otherAppIndex(OTHER_APPS_BASE_ID + 2, count = 2))
        assertNull(otherAppIndex(OTHER_APPS_BASE_ID - 1, count = 2))
        assertNull(otherAppIndex(Menu.FIRST, count = 2))
    }

    @Test
    fun processTextIntent_carriesTextAndSaysReadOnly() {
        val action = OtherAppAction("Traduzir", "com.exemplo.tradutor", "com.exemplo.tradutor.Tela")
        val intent = processTextIntent(action, "ls: invalid option -- '2'")

        assertEquals(Intent.ACTION_PROCESS_TEXT, intent.action)
        assertEquals("text/plain", intent.type)
        assertEquals(action.packageName, intent.component?.packageName)
        assertEquals(action.className, intent.component?.className)
        assertEquals("ls: invalid option -- '2'", intent.getCharSequenceExtra(Intent.EXTRA_PROCESS_TEXT))
        // Read-only, because the grid is a projection of what the remote
        // program printed — no outside app may rewrite it.
        assertTrue(intent.getBooleanExtra(Intent.EXTRA_PROCESS_TEXT_READONLY, false))
    }

    @Test
    fun shareIntent_isSystemPlainTextSend() {
        val chooser = shareTextIntent("erro do build")

        assertEquals(Intent.ACTION_CHOOSER, chooser.action)
        val sent = chooser.getParcelableExtra(Intent.EXTRA_INTENT, Intent::class.java)
        assertEquals(Intent.ACTION_SEND, sent?.action)
        assertEquals("text/plain", sent?.type)
        assertEquals("erro do build", sent?.getStringExtra(Intent.EXTRA_TEXT))
    }

    @Test
    fun anItemNeverHasBothLabels_norNeither() {
        val error = runCatching {
            BarItem(SelectionAction.COPY, Placement.BAR)
        }.exceptionOrNull()

        assertTrue("item sem rótulo nenhum apareceria em branco na barra", error is IllegalArgumentException)
    }
}
