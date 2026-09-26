package dev.servercontrolpanel.feature.auth.dashboard

import android.content.Context

/**
 * The dashboard tiles chosen on this device, in order.
 *
 * Stored locally, not on the server: different devices want different tiles,
 * a server preference would need a new idempotent write route, and local
 * storage works offline. Only ids are kept, so stale labels never linger and
 * removed tiles simply stop matching.
 */
internal object ChosenTiles {

    private const val FILE = "panel_dashboard_tiles"
    private const val KEY = "ids"
    private const val KEY_ALREADY_CHOSE = "assembled"

    /** Unit separator (0x1F), which never occurs in a tile id. */
    private const val SEPARATOR = "\u001F"

    /** Tile limit; beyond twelve the grid stops being readable at a glance. */
    const val MAX = 12

    /**
     * Returns [INITIAL_TILES] until the user first arranges the dashboard, then
     * their choice, even if they removed everything.
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

    /** Stores the choice and marks that one was made (see [read]). */
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
