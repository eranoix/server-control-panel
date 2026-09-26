package dev.servercontrolpanel.feature.terminal.selection

import android.view.Menu
import android.view.MenuItem

/**
 * Where an action appears on the system floating toolbar: on the bar or behind
 * the overflow button. The only hierarchy the toolbar has is each item's
 * `showAsAction`, which AOSP resolves counter-intuitively:
 *
 * 1. `FloatingToolbar.doShow()` drops items that are not `isVisible() && isEnabled()`
 *    and sorts the rest: `requiresActionButton()` first, `requiresOverflow()` last,
 *    ties by `getOrder()`.
 * 2. `ALWAYS` moves to the front and `NEVER` goes to the overflow.
 * 3. `layoutMainPanelItems()` fills the bar button by button and stops at the
 *    first item that asks for overflow.
 *
 * So [BAR] = `SHOW_AS_ACTION_ALWAYS` and [OVERFLOW] = `SHOW_AS_ACTION_NEVER`.
 * `ALWAYS` prioritises but does not reserve space: with a 400 dp bar and a 60 dp
 * overflow button, two labels fit comfortably, hence two actions on the bar (as
 * the `SHOW_AS_ACTION_ALWAYS` javadoc recommends and Termux does).
 */
enum class Placement { BAR, OVERFLOW }

/**
 * The actions the selection toolbar offers, with their menu order.
 *
 * The numbers are AOSP's (`Editor.java`: copy 5, paste 6, share 7, select all 8,
 * up to 11), so actions appear in the same sequence as in any text field.
 * [SEND_TO_TERMINAL] is 12, after AOSP's last item.
 *
 * Left out on purpose: Cut (the grid is not an editable buffer), Paste as plain
 * text (pasting sends bytes, see [PasteAction]), Replace/Autofill/Undo (no
 * `Editable`), "copy without line breaks" ([extractSelectedText] already joins
 * soft-wrapped lines), "copy previous command" (needs OSC 133 shell integration)
 * and history search (a screen, not a menu item).
 */
enum class SelectionAction(val order: Int) {
    /** Sends the selected text to the device clipboard. */
    COPY(5),

    /**
     * Sends the clipboard to the remote program as bytes on its input; there is no
     * editing cursor as in a text field.
     */
    PASTE(6),

    /** Hands the selected text to another app (`ACTION_SEND`), e.g. to share a build error. */
    SHARE(7),

    /** The whole grid. */
    SELECT_ALL(8),

    /**
     * Sends the selected text to the remote program's input, as if pasted: what is
     * on screen (a path, a suggested command, a container id) is often what runs
     * next. One tap instead of copy, dismiss, paste.
     */
    SEND_TO_TERMINAL(12),
}

/**
 * One resolved toolbar item: action, placement and label. [systemTitle] comes
 * from `android.R.string.*` (already translated by the system) where it exists;
 * otherwise (there is no public `android.R.string.share`) [customTitle] is used.
 */
data class BarItem(
    val action: SelectionAction,
    val highlight: Placement,
    val systemTitle: Int? = null,
    val customTitle: String? = null,
) {
    init {
        require((systemTitle == null) != (customTitle == null)) {
            "each item has exactly one label, the system's or ours ($action)"
        }
    }

    /** Stable id of the item within the `Menu`. */
    val id: Int get() = Menu.FIRST + action.ordinal

    /** Position in the menu; see [SelectionAction] for where the numbers come from. */
    val order: Int get() = action.order
}

/**
 * The whole toolbar, always the same. No item is conditional: a disabled item
 * disappears rather than greying out (`getVisibleAndEnabledMenuItems()`), so a
 * "Paste" hidden on an empty clipboard would change the toolbar's shape between
 * uses. The empty case is handled on tap ([PasteAction] says so).
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
 * Writes [items] and [fromOtherApps] into a real `Menu`. Other apps' items always
 * use `NEVER`, as in AOSP, so they never compete with Copy and Paste. The overflow
 * panel shows at most 4 rows before scrolling, leaving one for the first external app.
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
