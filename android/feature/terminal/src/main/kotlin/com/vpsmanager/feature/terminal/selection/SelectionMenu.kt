package com.vpsmanager.feature.terminal.selection

import android.view.Menu
import android.view.MenuItem

/**
 * Where an action shows up on the system floating toolbar: in the body of the
 * bar, or behind the overflow button (the three dots).
 *
 * This is not decoration — it is the only axis of hierarchy the floating
 * toolbar has, and what decides it is each item's `showAsAction`. AOSP
 * resolves it in three steps, and it is worth recording because the outcome is
 * counter-intuitive:
 *
 * 1. `FloatingToolbar.doShow()` discards the items that are not
 *    `isVisible() && isEnabled()` and sorts the rest by `showAsAction` class
 *    (`mMenuItemComparator`): first the ones answering `requiresActionButton()`,
 *    then the middle ones, last those of `requiresOverflow()`; ties broken
 *    by `getOrder()`.
 * 2. `MenuItemImpl.requiresActionButton()` is `showAsAction and ALWAYS == ALWAYS`;
 *    `requiresOverflow()` is `!requiresActionButton() && !requestsActionButton()`
 *    — in other words, **`ALWAYS` moves to the front of the queue and `NEVER`
 *    is sent to the overflow**.
 * 3. `LocalFloatingToolbarPopup.layoutMainPanelItems()` fills the main panel
 *    measuring button by button, **stops at the first item that asks for
 *    overflow**, and drops the rest into the `⋮` panel.
 *
 * So the hierarchy the app owner asked for — "copy and paste always show and
 * the other functions live inside the three dots" — is written exactly as
 * [BAR] = `SHOW_AS_ACTION_ALWAYS` and [OVERFLOW] = `SHOW_AS_ACTION_NEVER`.
 *
 * **`ALWAYS` prioritises, it does not reserve.** The bar has a preferred width
 * of 400dp (`config_floatingToolbarPreferredWidth`) and the overflow button
 * eats 60dp: two labels fit comfortably, three are a squeeze. Hence TWO actions
 * on the bar — the same number that `MenuItem.SHOW_AS_ACTION_ALWAYS`'s own
 * javadoc recommends ("no more than 2 items set to always show at a time"), and
 * the same one Termux uses (`Copy | Paste | ⋮`).
 */
enum class Placement { BAR, OVERFLOW }

/**
 * The actions the terminal's selection bar offers, together with the position
 * each one takes in the menu.
 *
 * **The numbers are not arbitrary: they are AOSP's.** `Editor.java` declares
 * `ACTION_MODE_MENU_ITEM_ORDER_CUT = 4`, `_COPY = 5`, `_PASTE = 6`, `_SHARE = 7`,
 * `_SELECT_ALL = 8`, `_REPLACE = 9`, `_AUTOFILL = 10`,
 * `_PASTE_AS_PLAIN_TEXT = 11`. Reusing the same numbering makes the terminal's
 * bar present its actions in the SAME sequence as any text field on the
 * device — which is the only way for the user to find each one without
 * reading. [SEND_TO_TERMINAL] sits at 12, right after AOSP's last item,
 * because it is ours and should not interleave with theirs.
 *
 * **What was left out, and why.** A text field also offers Cut, Replace…,
 * Paste as plain text and Autofill. None of that exists here:
 * - **Cut** removes text from an editable buffer. The grid is the projection of
 *   what the remote program printed — there is nothing to remove, and a "cut"
 *   that merely copied would be a button that lies.
 * - **Paste as plain text** exists to discard formatting. What goes into the
 *   terminal are bytes ([PasteAction]); there never was any formatting to
 *   discard, so it would be a second "Paste" under another name.
 * - **Replace… / Autofill / Undo / Redo** depend on an `Editable` with history
 *   and on a `View` with an autofill id. This screen has neither, by
 *   architectural decision (see `TerminalInputView`).
 *
 * **And the terminal-specific ideas that were also left out:**
 * - **"Copy without line breaks"**, to paste a command the screen broke across
 *   several lines: no longer needed. [extractSelectedText] uses the SOFT wrap
 *   marks from the snapshot and joins those lines on its own — a long command
 *   the terminal folded already comes out as a single line. What would be left
 *   for that action to join are `\n` the program genuinely printed, and turning
 *   them into spaces would silently corrupt any stretch of log.
 * - **"Copy the previous command"** requires knowing where the prompt begins
 *   and ends, which is only knowable with shell integration (the OSC 133
 *   marks). The emulator does not process them today; without them, any
 *   heuristic for "where the prompt was" gets it wrong in `vim`, in `htop` and
 *   in any TUI.
 * - **"Search the history"** is not an action on the selection: it is a screen,
 *   with a search field, navigation between hits and highlighting. It does not
 *   fit in a menu item, and cramming it in here would be hiding a whole
 *   feature behind a `⋮`.
 */
enum class SelectionAction(val order: Int) {
    /** Sends the selected text to the device clipboard. */
    COPY(5),

    /**
     * Sends the clipboard to the remote program. It stays on the bar at the
     * app owner's request, and it is not the same "Paste" as a text field's:
     * there is no editing cursor, the text becomes bytes on the program's
     * input.
     */
    PASTE(6),

    /**
     * Hands the selected text to another app (`ACTION_SEND`). This is what
     * turns a build error or a stretch of log into a message, without going
     * through the clipboard. Today's ConnectBot has exactly this item in the
     * `⋮` of its own selection bar.
     */
    SHARE(7),

    /** The whole grid. */
    SELECT_ALL(8),

    /**
     * Gives the selected text back TO THE INPUT of the remote program, as
     * though it had been pasted. It is the only action here that a text field
     * has no way of having: in a terminal, what is on screen is usually
     * exactly what you want to run next — the path `ls` has just printed, the
     * command the error message suggests, the container id `docker ps` listed.
     * Without this the route is copy, dismiss the bar, paste; with it, it is
     * one tap.
     */
    SEND_TO_TERMINAL(12),
}

/**
 * One item of the bar, already resolved: which action, where it appears and
 * under what label.
 *
 * [systemTitle] comes from `android.R.string.*` wherever one exists: they
 * are the SAME strings, already translated into the device's language, that
 * Android's own search field shows. Where the system exposes none — there is
 * no public `android.R.string.share`, checked against the SDK 37 `android.jar`
 * — the label is ours, in [customTitle].
 */
data class BarItem(
    val action: SelectionAction,
    val highlight: Placement,
    val systemTitle: Int? = null,
    val customTitle: String? = null,
) {
    init {
        require((systemTitle == null) != (customTitle == null)) {
            "cada item tem exatamente um rótulo: ou o do sistema, ou o nosso ($action)"
        }
    }

    /** Stable id of the item within the `Menu`. */
    val id: Int get() = Menu.FIRST + action.ordinal

    /** Position in the menu — see [SelectionAction] on where these numbers come from. */
    val order: Int get() = action.order
}

/**
 * The whole bar, always the same one.
 *
 * **There is no conditional item here, and that is a decision, not an
 * oversight.** The temptation was to hide "Paste" when the clipboard is empty
 * — it is what the code used to do, via `MenuItem.setEnabled(false)`, and it is
 * what Termux still does today. Except that
 * `FloatingToolbar.getVisibleAndEnabledMenuItems()` filters by
 * `isVisible() && isEnabled()`: a disabled item does not go grey, it
 * DISAPPEARS. The effect was a bar that changed shape between one use and the
 * next — "Copy | Select all" one moment, "Copy | Paste | Select all" the next —
 * leaving the user's finger nowhere to build muscle memory.
 *
 * So the bar is fixed and the empty case is handled where it really belongs:
 * on the tap ([PasteAction] says so instead of silently doing nothing).
 */
fun barItems(): List<BarItem> = listOf(
    BarItem(SelectionAction.COPY, Placement.BAR, systemTitle = android.R.string.copy),
    BarItem(SelectionAction.PASTE, Placement.BAR, systemTitle = android.R.string.paste),
    BarItem(SelectionAction.SHARE, Placement.OVERFLOW, customTitle = "Share"),
    BarItem(
        SelectionAction.SELECT_ALL,
        Placement.OVERFLOW,
        systemTitle = android.R.string.selectAll,
    ),
    BarItem(
        SelectionAction.SEND_TO_TERMINAL,
        Placement.OVERFLOW,
        customTitle = "Send to terminal",
    ),
)

/** The action [id] identifies, or `null` if the id is not one of ours. */
fun actionForItem(id: Int): SelectionAction? = SelectionAction.entries.getOrNull(id - Menu.FIRST)

/**
 * The index of the external app that [id] identifies, or `null` if the id is
 * not in that range. See [OtherAppAction.id].
 */
fun otherAppIndex(id: Int, count: Int): Int? =
    (id - OTHER_APPS_BASE_ID).takeIf { it in 0 until count }

/**
 * Writes [items] and [fromOtherApps] into a real `Menu`.
 *
 * The `showAsAction` comes straight from the [Placement]; items from other apps
 * always go in as `NEVER`, as in AOSP — what comes from outside never competes
 * for a place on the bar with Copy and Paste.
 *
 * A note on capacity: the overflow panel shows at most 4 rows at a time
 * (`LocalFloatingToolbarPopup.MAX_OVERFLOW_SIZE`) and scrolls beyond that. With
 * our three overflow items, exactly one row is left for the first external app
 * before the panel starts scrolling — which is why the fixed set stopped at
 * three.
 */
fun buildMenu(
    menu: Menu,
    items: List<BarItem> = barItems(),
    fromOtherApps: List<OtherAppAction> = emptyList(),
) {
    for (item in items) {
        val entry = if (item.systemTitle != null) {
            menu.add(Menu.NONE, item.id, item.order, item.systemTitle)
        } else {
            menu.add(Menu.NONE, item.id, item.order, item.customTitle)
        }
        entry.setShowAsAction(
            when (item.highlight) {
                Placement.BAR -> MenuItem.SHOW_AS_ACTION_ALWAYS
                Placement.OVERFLOW -> MenuItem.SHOW_AS_ACTION_NEVER
            },
        )
    }
    fromOtherApps.forEachIndexed { index, action ->
        menu.add(Menu.NONE, action.id(index), OTHER_APPS_ORDER + index, action.label)
            .setShowAsAction(MenuItem.SHOW_AS_ACTION_NEVER)
    }
}
