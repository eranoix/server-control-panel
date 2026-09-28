package dev.servercontrolpanel.feature.auth.dashboard

import android.content.Context

internal object ChosenTiles {

    private const val FILE = "panel_dashboard_tiles"
    private const val KEY = "ids"
    private const val KEY_ALREADY_CHOSE = "assembled"

    private const val SEPARATOR = "\u001F"

    const val MAX = 12

    fun read(context: Context): List<String> {
        val p = prefs(context)
        if (!p.getBoolean(KEY_ALREADY_CHOSE, false)) return INITIAL_TILES
        return p.getString(KEY, "")
            ?.split(SEPARATOR)
            ?.filter { it.isNotBlank() }
            ?.take(MAX)
            .orEmpty()
    }

    fun persist(context: Context, ids: List<String>) {
        prefs(context).edit()
            .putString(KEY, ids.distinct().take(MAX).joinToString(SEPARATOR))
            .putBoolean(KEY_ALREADY_CHOSE, true)
            .apply()
    }

    fun append(context: Context, id: String): List<String> {
        val current = read(context)
        if (id in current || current.size >= MAX) return current
        val next = current + id
        persist(context, next)
        return next
    }

    fun remove(context: Context, id: String): List<String> {
        val next = read(context).filterNot { it == id }
        persist(context, next)
        return next
    }

    private fun prefs(context: Context) =
        context.getSharedPreferences(FILE, Context.MODE_PRIVATE)
}
