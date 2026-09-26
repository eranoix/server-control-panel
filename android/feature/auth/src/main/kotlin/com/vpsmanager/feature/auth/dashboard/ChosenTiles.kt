package com.vpsmanager.feature.auth.dashboard

import android.content.Context

/**
 * The blocks this person chose for their dashboard, on this device.
 *
 * ## Why the choice is local, and not the server's
 *
 * It is tempting to keep this on the server so the dashboard follows the
 * person between devices. It would be the wrong decision, for two concrete
 * reasons. The first is that a phone's dashboard and a 27" monitor's do not
 * want the same blocks — syncing would impose one context's choice on the
 * other. The second is harder: a preference on the server needs a write route,
 * and a new write route needs idempotency before the outbox can carry it
 * offline. Keeping it here costs nothing and works with no network, which is
 * exactly when a dashboard needs to open.
 *
 * ## Why ids only, and in ORDER
 *
 * The order is the choice — it is what puts what matters on the first row. And
 * ids only, for the same reason as `AdminRecents`: label and value belong to
 * the server and go stale; an id that has left the catalogue simply does not
 * match and the block disappears, without leaving a gravestone on screen.
 */
internal object ChosenTiles {

    private const val FILE = "vpsm_painel_blocos"
    private const val KEY = "ids"
    private const val KEY_ALREADY_CHOSE = "montado"

    /** Unit separator (US, 0x1F) — never occurs inside a block id. */
    private const val SEPARATOR = "\u001F"

    /**
     * The ceiling on blocks. Twelve, because past that the grid stops being a
     * dashboard and becomes a list in disguise — which is exactly this
     * model's known weakness, and the reason it loses to a mosaic in the "too
     * many" scenario. A ceiling is more honest than letting someone assemble
     * forty blocks and find out for themselves that it cannot be read.
     */
    const val MAX = 12

    /**
     * What to show. Before the first arrangement it returns [INITIAL_TILES]
     * — a dashboard born empty forces you to assemble before seeing any value,
     * and someone opening a dashboard for the first time wants to see their
     * server, not a catalogue.
     *
     * Once the person has touched it, their choice rules, **including when
     * they remove everything**. Returning to the starting set in that case
     * would be the app undoing what they have just done.
     */
    fun read(context: Context): List<String> {
        val p = prefs(context)
        if (!p.getBoolean(KEY_ALREADY_CHOSE, false)) return INITIAL_TILES
        return p.getString(KEY, "")
            ?.split(SEPARATOR)
            ?.filter { it.isNotBlank() }
            ?.take(MAX)
            .orEmpty()
    }

    /** Stores the choice and records that it exists — see [read]. */
    fun persist(context: Context, ids: List<String>) {
        prefs(context).edit()
            .putString(KEY, ids.distinct().take(MAX).joinToString(SEPARATOR))
            .putBoolean(KEY_ALREADY_CHOSE, true)
            .apply()
    }

    /** Appends [id] at the end, if there is room. Returns the new list. */
    fun append(context: Context, id: String): List<String> {
        val current = read(context)
        if (id in current || current.size >= MAX) return current
        val next = current + id
        persist(context, next)
        return next
    }

    /** Removes [id]. Returns the new list. */
    fun remove(context: Context, id: String): List<String> {
        val next = read(context).filterNot { it == id }
        persist(context, next)
        return next
    }

    private fun prefs(context: Context) =
        context.getSharedPreferences(FILE, Context.MODE_PRIVATE)
}
