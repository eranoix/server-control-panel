package dev.servercontrolpanel.feature.terminal.selection

import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.content.pm.ResolveInfo
import android.view.Menu

data class OtherAppAction(
    val label: String,
    val packageName: String,
    val className: String,
) {
    fun id(index: Int): Int = OTHER_APPS_BASE_ID + index
}

const val OTHER_APPS_BASE_ID = Menu.FIRST + 1000

const val OTHER_APPS_ORDER = 100

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

internal fun baseProcessTextIntent(): Intent =
    Intent(Intent.ACTION_PROCESS_TEXT).setType("text/plain")

fun processTextIntent(action: OtherAppAction, text: String): Intent =
    baseProcessTextIntent()
        .setClassName(action.packageName, action.className)
        .putExtra(Intent.EXTRA_PROCESS_TEXT, text)
        .putExtra(Intent.EXTRA_PROCESS_TEXT_READONLY, true)
