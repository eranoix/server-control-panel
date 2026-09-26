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
internal object AlertasVistos {

    private const val ARQUIVO = "vpsm_alertas_vistos"
    private const val CHAVE = "marcas"
    /** Unit separator (US, 0x1F). Escaped, never the literal byte. */
    private const val SEPARADOR = "\u001F"

    /**
     * The ceiling on stored marks. Without one, an alert oscillating around
     * its threshold would produce a new key per reading and the file would
     * grow forever. Fifty comfortably covers a day's triage.
     */
    private const val MAXIMO = 50

    /**
     * An alert's identity FOR THE PURPOSE OF SILENCING IT.
     *
     * Name plus rounded value. The rounding exists because a disk-usage alert
     * drifts from `86.3` to `86.4` on its own, and without it the mark would
     * come undone on every reading — "seen" would never last a minute.
     */
    fun chaveDe(alerta: OpsAlert): String = "${alerta.name}@${Math.round(alerta.currentValue)}"

    fun ler(context: Context): Set<String> =
        prefs(context).getString(CHAVE, null)
            ?.split(SEPARADOR)
            ?.filter { it.isNotBlank() }
            ?.toSet()
            .orEmpty()

    fun marcar(context: Context, alerta: OpsAlert): Set<String> {
        val nova = (listOf(chaveDe(alerta)) + ler(context)).distinct().take(MAXIMO)
        prefs(context).edit().putString(CHAVE, nova.joinToString(SEPARADOR)).apply()
        return nova.toSet()
    }

    fun desmarcarTudo(context: Context): Set<String> {
        prefs(context).edit().remove(CHAVE).apply()
        return emptySet()
    }

    private fun prefs(context: Context) =
        context.getSharedPreferences(ARQUIVO, Context.MODE_PRIVATE)
}
