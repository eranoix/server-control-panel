package dev.servercontrolpanel.feature.terminal.power

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.PowerManager
import android.provider.Settings
import androidx.core.content.getSystemService

const val BATTERY_EXEMPTION_LABEL = "Keep the session when switching apps"

const val BATTERY_EXEMPTION_TAG = "battery-exemption-button"

const val BATTERY_EXEMPTION_DIALOG_TAG = "battery-exemption-dialog"

const val BATTERY_EXEMPTION_EXPLANATION: String =
    "When you leave the app, Android cuts its network within a few seconds and the " +
        "terminal session drops — that is why it reconnects every time you come back.\n\n" +
        "Exempting this app from battery optimization lets it hold on for about a " +
        "minute off screen instead of a few seconds: switching apps, reading a " +
        "message and coming back no longer costs a reconnect.\n\n" +
        "It is not forever: after roughly a minute Android freezes the " +
        "app anyway and the connection drops — then it comes back on its own, and the session " +
        "stays intact on the server, without losing anything that was running."

fun isBatteryOptimizationExempt(context: Context): Boolean {
    val power = context.getSystemService<PowerManager>() ?: return false
    return power.isIgnoringBatteryOptimizations(context.packageName)
}

fun openBatteryExemptionRequest(context: Context): Boolean {
    val directRequest = Intent(
        Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS,
        Uri.parse("package:${context.packageName}"),
    )
    if (start(context, directRequest)) return true
    return start(context, Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
}

private fun start(context: Context, intent: Intent): Boolean {
    if (context !is Activity) intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
    return runCatching { context.startActivity(intent) }.isSuccess
}
