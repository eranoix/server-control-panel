package dev.servercontrolpanel.feature.terminal.power

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
 * The text shown BEFORE the system prompt. Honest about its reach: measured on an
 * Android 16 emulator, the exemption extends background survival from about 6 s
 * to about 70 s; past that the cached-app freezer still kills the connection.
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
 * Opens the system request (`ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS`). Call
 * it only after [BATTERY_EXEMPTION_EXPLANATION] was read: a context-free dialog is
 * denied by reflex and not offered again.
 *
 * Falls back to the settings list when an OEM removed the direct action. Returns
 * `false` when neither opened, so the caller can say so.
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
    // FLAG_ACTIVITY_NEW_TASK only outside an Activity; inside one it gets in the
    // way of returning to the screen.
    if (context !is Activity) intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
    return runCatching { context.startActivity(intent) }.isSuccess
}
