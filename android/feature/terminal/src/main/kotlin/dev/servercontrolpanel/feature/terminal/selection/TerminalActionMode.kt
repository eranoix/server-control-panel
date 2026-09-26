package dev.servercontrolpanel.feature.terminal.selection

import android.view.ActionMode
import android.view.Menu
import android.view.MenuItem
import android.view.View
import androidx.compose.ui.geometry.Rect
import kotlin.math.roundToInt

/**
 * The system floating toolbar over the terminal: an `ActionMode` of type
 * [ActionMode.TYPE_FLOATING], the same one text fields use. It brings anchoring to
 * the selection, animation, the overflow panel and translated labels
 * (`android.R.string.*`). Other apps' actions ("Translate", "Search") are not
 * included by default (in AOSP the `Editor` adds them), so [otherAppActions]
 * replicates `ProcessTextIntentActionsHandler`.
 *
 * Which actions go where is decided by [barItems]; read [Placement] first, since
 * AOSP interprets `showAsAction` differently from `Toolbar`. Compose's
 * `LocalTextToolbar` needs a text field, which this screen deliberately lacks (see
 * `TerminalInputView`), so the mode is started from the `View`.
 *
 * @param host the [dev.servercontrolpanel.feature.terminal.input.TerminalInputView], which
 *   covers exactly the grid, so the selection rectangle goes straight to
 *   `onGetContentRect` without conversion.
 */
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

    // `finish()` fires `onDestroyActionMode`, whose listener usually clears the
    // selection and calls `hide()` again; this latch prevents infinite recursion.
    private var closingFromInside = false

    // Other apps are frozen when the menu is built and clicks resolve by index,
    // so a re-query returning a different order cannot fire the wrong app.
    private var otherAppsInMenu: List<OtherAppAction> = emptyList()

    /** Shows the bar, or merely repositions the one already up. */
    fun show() {
        val current = actionMode
        if (current != null) {
            current.invalidateContentRect()
            return
        }
        actionMode = host.startActionMode(callback, ActionMode.TYPE_FLOATING)
    }

    /**
     * The selection changed size or position: re-anchor the toolbar. The system
     * also hides it temporarily during a handle drag, like in a text field.
     */
    fun update() {
        actionMode?.invalidateContentRect()
    }

    /** Whether the toolbar is showing. Only the instrumented test asks. */
    internal fun isShowing(): Boolean = actionMode != null

    /**
     * The `Menu` the system built, read only by the instrumented test to check the
     * real menu and click through `performIdentifierAction`.
     */
    internal fun menuShown(): Menu? = actionMode?.menu

    /** Takes the toolbar down without notifying whoever requested it. */
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

        /**
         * `false`: the menu is unchanged between showings (see [barItems]). Never
         * read the clipboard here: disabled items vanish instead of greying out,
         * and since Android 12 `getPrimaryClip()` shows a "pasted from your
         * clipboard" notice on every selection.
         */
        override fun onPrepareActionMode(mode: ActionMode, menu: Menu): Boolean = false

        override fun onActionItemClicked(mode: ActionMode, item: MenuItem): Boolean =
            when (actionForItem(item.itemId)) {
                SelectionAction.COPY -> {
                    onCopy()
                    // Copying ends the selection, as in a text field.
                    mode.finish()
                    true
                }
                SelectionAction.SELECT_ALL -> {
                    // The only action that keeps the toolbar: the next step
                    // (copy, share) acts on the new selection.
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
                // Not ours: maybe another app's action (`ACTION_PROCESS_TEXT`);
                // otherwise `false` lets the system handle it.
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

        /**
         * Where the selection is, so the system places the toolbar above (or below)
         * it; by default it would use the whole grid and appear at the top.
         */
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
