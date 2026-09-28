package dev.servercontrolpanel.feature.notifications.inbox

import android.content.Context
import dev.servercontrolpanel.data.ops.OpsAlert

internal object SeenAlerts {

    private const val FILE = "panel_seen_alerts"
    private const val KEY = "marks"
    private const val SEPARATOR = "\u001F"

    private const val MAX = 50

    fun keyOf(alert: OpsAlert): String = "${alert.name}@${Math.round(alert.currentValue)}"

    fun read(context: Context): Set<String> =
        prefs(context).getString(KEY, null)
            ?.split(SEPARATOR)
            ?.filter { it.isNotBlank() }
            ?.toSet()
            .orEmpty()

    fun mark(context: Context, alert: OpsAlert): Set<String> {
        val next = (listOf(keyOf(alert)) + read(context)).distinct().take(MAX)
        prefs(context).edit().putString(KEY, next.joinToString(SEPARATOR)).apply()
        return next.toSet()
    }

    fun unmarkAll(context: Context): Set<String> {
        prefs(context).edit().remove(KEY).apply()
        return emptySet()
    }

    private fun prefs(context: Context) =
        context.getSharedPreferences(FILE, Context.MODE_PRIVATE)
}
