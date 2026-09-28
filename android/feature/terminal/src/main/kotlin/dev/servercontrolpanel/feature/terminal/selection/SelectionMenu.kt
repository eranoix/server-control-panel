package dev.servercontrolpanel.feature.terminal.selection

import android.view.Menu
import android.view.MenuItem

enum class Placement { BAR, OVERFLOW }

enum class SelectionAction(val order: Int) {
    COPY(5),

    PASTE(6),

    SHARE(7),

    SELECT_ALL(8),

    SEND_TO_TERMINAL(12),
}

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

    val id: Int get() = Menu.FIRST + action.ordinal

    val order: Int get() = action.order
}

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

fun actionForItem(id: Int): SelectionAction? = SelectionAction.entries.getOrNull(id - Menu.FIRST)

fun otherAppIndex(id: Int, count: Int): Int? =
    (id - OTHER_APPS_BASE_ID).takeIf { it in 0 until count }

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
