package dev.servercontrolpanel.feature.admin

import android.content.Context

internal object AdminRecents {

    const val MAX = 6

    private const val FILE = "panel_admin_recents"
    private const val KEY = "ids"
    private const val SEPARATOR = "\u001F"

    fun read(context: Context): List<String> =
        prefs(context).getString(KEY, null)
            ?.split(SEPARATOR)
            ?.filter { it.isNotBlank() }
            ?.take(MAX)
            .orEmpty()

    fun registrar(context: Context, sectionId: String) {
        if (sectionId.isBlank()) return
        val next = (listOf(sectionId) + read(context).filterNot { it == sectionId }).take(MAX)
        prefs(context).edit().putString(KEY, next.joinToString(SEPARATOR)).apply()
    }

    private fun prefs(context: Context) =
        context.applicationContext.getSharedPreferences(FILE, Context.MODE_PRIVATE)
}
