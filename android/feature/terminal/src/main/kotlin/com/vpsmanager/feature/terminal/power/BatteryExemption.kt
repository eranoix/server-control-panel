package com.vpsmanager.feature.terminal.power

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.PowerManager
import android.provider.Settings
import androidx.core.content.getSystemService

/** Label of the item that opens the explanation, in the terminal's options sheet. */
const val BATTERY_EXEMPTION_LABEL = "Keep the session when switching apps"

/** Test tag for the sheet item. */
const val BATTERY_EXEMPTION_TAG = "botao-isencao-bateria"

/** Test tag for the dialog that explains before the system asks. */
const val BATTERY_EXEMPTION_DIALOG_TAG = "dialogo-isencao-bateria"

/**
 * The text the operator reads BEFORE the system asks anything.
 *
 * It is deliberately honest about its reach. The exemption does NOT keep the
 * connection alive indefinitely: measured on an Android 16 emulator, it takes
 * background survival from about 6 s to about 70 s, and past those 70 s what
 * kills it is the cached-app freezer, which the exemption does not turn off.
 * Promising "keeps your connection alive" would be selling what Android does
 * not deliver — and the person would discover the lie over their first
 * ten-minute coffee.
 */
const val BATTERY_EXEMPTION_EXPLANATION: String =
    "When you leave the app, Android cuts its network within a few seconds and the " +
        "terminal session drops — that is why it reconnects every time you come back.\n\n" +
        "Exempting this app from battery optimization lets it hold on for about a " +
        "minute off screen instead of a few seconds: switching apps, reading a " +
        "message and coming back no longer costs a reconnect.\n\n" +
        "It is not forever: after roughly a minute Android freezes the " +
        "app anyway and the connection drops — then it comes back on its own, and the session " +
        "stays intact on the server, without losing anything that was running."

/** Already exempt? Asks the system, never a cached guess. */
fun isBatteryOptimizationExempt(context: Context): Boolean {
    val power = context.getSystemService<PowerManager>() ?: return false
    return power.isIgnoringBatteryOptimizations(context.packageName)
}

/**
 * Opens the SYSTEM's request (`ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS`).
 *
 * It should only be called after [BATTERY_EXEMPTION_EXPLANATION] has been read: a
 * system dialog with no context is denied by reflex, and once denied it is
 * never offered again on its own.
 *
 * Falls back to the list screen
 * (`ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS`) when the direct request does
 * not exist on the device — some OEMs remove the direct action. Returns
 * `false` when neither opened, so the caller can say something instead of
 * flickering to no effect.
 */
fun openBatteryExemptionRequest(context: Context): Boolean {
    val directRequest = Intent(
        Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS,
        Uri.parse("package:${context.packageName}"),
    )
    if (start(context, directRequest)) return true
    return start(context, Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
}

private fun start(context: Context, intent: Intent): Boolean {
    // FLAG_ACTIVITY_NEW_TASK only when the context is not an Activity: inside
    // an Activity the flag is unnecessary and gets in the way of coming back
    // to the screen.
    if (context !is Activity) intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
    return runCatching { context.startActivity(intent) }.isSuccess
}
