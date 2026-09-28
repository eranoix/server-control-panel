package dev.servercontrolpanel.feature.terminal.selection

import android.view.ActionMode
import android.view.Menu
import android.view.MenuItem
import android.view.View
import androidx.compose.ui.geometry.Rect
import kotlin.math.roundToInt

class TerminalActionMode(
    private val host: View,
    private val onCopy: () -> Unit,
    private val onSelectAll: () -> Unit,
    private val onPaste: () -> Unit,
    private val onShare: () -> Unit,
    private val onSendToTerminal: () -> Unit,
    private val onClose: () -> Unit,
    private val selectionRect: () -> Rect,
    private val otherAppActions: () -> List<OtherAppAction> = ::emptyList,
    private val onUseOtherApp: (OtherAppAction) -> Unit = {},
) {

    private var actionMode: ActionMode? = null

    private var closingFromInside = false

    private var otherAppsInMenu: List<OtherAppAction> = emptyList()

    fun show() {
        val current = actionMode
        if (current != null) {
            current.invalidateContentRect()
            return
        }
        actionMode = host.startActionMode(callback, ActionMode.TYPE_FLOATING)
    }

    fun update() {
        actionMode?.invalidateContentRect()
    }

    internal fun isShowing(): Boolean = actionMode != null

    internal fun menuShown(): Menu? = actionMode?.menu

    fun hide() {
        val current = actionMode ?: return
        actionMode = null
        closingFromInside = true
        current.finish()
        closingFromInside = false
    }

    private val callback = object : ActionMode.Callback2() {
        override fun onCreateActionMode(mode: ActionMode, menu: Menu): Boolean {
            otherAppsInMenu = otherAppActions()
            buildMenu(menu, fromOtherApps = otherAppsInMenu)
            return true
        }

        override fun onPrepareActionMode(mode: ActionMode, menu: Menu): Boolean = false

        override fun onActionItemClicked(mode: ActionMode, item: MenuItem): Boolean =
            when (actionForItem(item.itemId)) {
                SelectionAction.COPY -> {
                    onCopy()
                    mode.finish()
                    true
                }
                SelectionAction.SELECT_ALL -> {
                    onSelectAll()
                    true
                }
                SelectionAction.PASTE -> {
                    onPaste()
                    mode.finish()
                    true
                }
                SelectionAction.SHARE -> {
                    onShare()
                    mode.finish()
                    true
                }
                SelectionAction.SEND_TO_TERMINAL -> {
                    onSendToTerminal()
                    mode.finish()
                    true
                }
                null -> {
                    val index = otherAppIndex(item.itemId, otherAppsInMenu.size)
                    if (index == null) {
                        false
                    } else {
                        onUseOtherApp(otherAppsInMenu[index])
                        mode.finish()
                        true
                    }
                }
            }

        override fun onDestroyActionMode(mode: ActionMode) {
            actionMode = null
            if (!closingFromInside) onClose()
        }

        override fun onGetContentRect(mode: ActionMode, view: View, outRect: android.graphics.Rect) {
            val r = selectionRect()
            outRect.set(
                r.left.roundToInt(),
                r.top.roundToInt(),
                r.right.roundToInt(),
                r.bottom.roundToInt(),
            )
        }
    }
}
