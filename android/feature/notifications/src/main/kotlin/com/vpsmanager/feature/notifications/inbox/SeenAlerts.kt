package com.vpsmanager.feature.notifications.inbox

import android.content.Context
import com.vpsmanager.data.ops.OpsAlert

/**
 * The alerts this person has already seen, **on this device**.
 *
 * ## Why local, and why the screen says so
 *
 * Truly acknowledging an alert — PagerDuty's "ack" — is SHARED state: when one
 * person on call acknowledges, the others stop being paged. That state lives
 * on the server, and the BFF has no route for it today.
 *
 * Inventing an "Acknowledge" that only exists on this device and not saying so
 * would be the worst class of lie this app could tell: on a team, the person
 * would believe they had told everyone else. So the label is **"Seen on this
 * device"** — it describes exactly what happens, and it stays useful to
 * someone operating alone, which is the real case here: silencing what you
 * have already looked at is half of triage.
 *
 * ## The mark undoes itself when the alert changes
 *
 * The key includes the alert's VALUE. Disk at 86% seen, disk at 94% is a
 * different alert — and it has to reappear. Without that, marking something
 * seen once would silence the same rule forever, which is how an alerting
 * system dies: not with an error, with silence.
 */
internal object SeenAlerts {

    private const val FILE = "vpsm_alertas_vistos"
    private const val KEY = "marcas"
    /** Unit separator (US, 0x1F). Escaped, never the literal byte. */
    private const val SEPARATOR = "\u001F"

    /**
     * The ceiling on stored marks. Without one, an alert oscillating around
     * its threshold would produce a new key per reading and the file would
     * grow forever. Fifty comfortably covers a day's triage.
     */
    private const val MAX = 50

    /**
     * An alert's identity FOR THE PURPOSE OF SILENCING IT.
     *
     * Name plus rounded value. The rounding exists because a disk-usage alert
     * drifts from `86.3` to `86.4` on its own, and without it the mark would
     * come undone on every reading — "seen" would never last a minute.
     */
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
