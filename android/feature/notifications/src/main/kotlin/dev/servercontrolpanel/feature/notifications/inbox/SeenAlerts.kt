package dev.servercontrolpanel.feature.notifications.inbox

import android.content.Context
import dev.servercontrolpanel.data.ops.OpsAlert

/**
 * Alerts already seen **on this device**.
 *
 * A real acknowledge is shared server state with no API yet, so this is labelled "Seen on this
 * device" to avoid implying the team was told.
 *
 * The key includes the alert's value, so a changed reading (disk 86% then 94%) reappears instead
 * of a rule being silenced forever.
 */
internal object SeenAlerts {

    private const val FILE = "panel_alertas_vistos"
    private const val KEY = "marcas"
    /** Unit separator (US, 0x1F), written escaped. */
    private const val SEPARATOR = "\u001F"

    /** Cap on stored marks, so an alert oscillating around its threshold cannot grow the file forever. */
    private const val MAX = 50

    /** Silencing identity: name plus rounded value, so small drift (86.3 to 86.4) keeps the mark. */
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
