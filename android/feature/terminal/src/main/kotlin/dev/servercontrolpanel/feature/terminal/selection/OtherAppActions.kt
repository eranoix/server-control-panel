package dev.servercontrolpanel.feature.terminal.selection

import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.content.pm.ResolveInfo
import android.view.Menu

/**
 * An action that ANOTHER installed app offers over the selected text —
 * "Translate", "Search", "Read aloud". That is how those items appear on the
 * bar of every Android text field, and there was no reason for the terminal to
 * be the one surface on the device where they vanish.
 *
 * The protocol is `Intent.ACTION_PROCESS_TEXT` with `text/plain`: the app
 * publishes an activity that receives the text in `EXTRA_PROCESS_TEXT` and
 * returns the processed text in the same extra. Here
 * `EXTRA_PROCESS_TEXT_READONLY` is always `true` — the terminal grid is the
 * projection of what the remote program printed, and no outside app may
 * rewrite it. Saying so in the intent is what makes a translator open in
 * read-only mode rather than offering to "replace".
 */
data class OtherAppAction(
    val label: String,
    val packageName: String,
    val className: String,
) {
    /**
     * Id in the `Menu`. It comes from a band of its own, well above the one
     * for our actions, so [actionForItem] never mistakes a third party's item for
     * one of ours — the defect would be "Translate" firing "Send to terminal".
     */
    fun id(index: Int): Int = OTHER_APPS_BASE_ID + index
}

/** Start of the id band reserved for outside apps. */
const val OTHER_APPS_BASE_ID = Menu.FIRST + 1000

/**
 * Order of the first third-party item in the menu.
 *
 * It is the same 100 as `Editor.ACTION_MODE_MENU_ITEM_ORDER_PROCESS_TEXT_INTENT_ACTIONS_START`
 * in AOSP, and the reason it is so high is the same: what comes from outside
 * goes in AFTER everything of ours, always, however many apps are installed.
 */
const val OTHER_APPS_ORDER = 100

/**
 * What the installed apps offer for plain text, in the order the system
 * resolves them.
 *
 * The two filters come from AOSP's
 * `Editor.ProcessTextIntentActionsHandler.isSupportedActivity`, and they are
 * not excessive caution: an activity that is not exported cannot be started by
 * us (the item would become a button that fails with a `SecurityException`),
 * and one demanding a permission the app does not hold, likewise.
 *
 * **It depends on `<queries>` in the manifest.** Since Android 11 the system
 * hides the list of other apps from each app; without the `ACTION_PROCESS_TEXT`
 * `<intent>` declared (see this module's `AndroidManifest.xml`) this query
 * comes back empty — silently, which is the most expensive way to find the
 * problem out.
 */
fun otherAppActions(context: Context): List<OtherAppAction> {
    val pm = context.packageManager
    return pm.queryIntentActivities(baseProcessTextIntent(), 0)
        .filter { isUsable(context, it) }
        .map {
            OtherAppAction(
                label = it.loadLabel(pm).toString(),
                packageName = it.activityInfo.packageName,
                className = it.activityInfo.name,
            )
        }
}

private fun isUsable(context: Context, info: ResolveInfo): Boolean {
    if (context.packageName == info.activityInfo.packageName) return true
    if (!info.activityInfo.exported) return false
    val permission = info.activityInfo.permission ?: return true
    return context.checkSelfPermission(permission) == PackageManager.PERMISSION_GRANTED
}

/** The "raw" intent, with no text and no target — used to ask who answers. */
internal fun baseProcessTextIntent(): Intent =
    Intent(Intent.ACTION_PROCESS_TEXT).setType("text/plain")

/**
 * The intent that opens [action] with [text].
 *
 * Assembled on the CLICK, never while building the menu: the selection changes
 * while the bar is up (dragging a handle, tapping "Select all") without the
 * system rebuilding the menu, so an intent prepared beforehand would carry the
 * wrong text. That is why `MenuItem.setIntent` is not used here, which is how
 * AOSP does it — there the `Editor` recreates the menu on every change of
 * selection; here it does not.
 */
fun processTextIntent(action: OtherAppAction, text: String): Intent =
    baseProcessTextIntent()
        .setClassName(action.packageName, action.className)
        .putExtra(Intent.EXTRA_PROCESS_TEXT, text)
        .putExtra(Intent.EXTRA_PROCESS_TEXT_READONLY, true)
