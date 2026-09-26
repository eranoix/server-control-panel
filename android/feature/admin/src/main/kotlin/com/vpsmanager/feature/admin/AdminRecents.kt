package com.vpsmanager.feature.admin

import android.content.Context

/**
 * Recently opened sections on this device.
 *
 * SharedPreferences rather than DataStore: `:feature-admin` does not depend on
 * DataStore, and access is tiny and rare. Only ids are stored, never labels, so
 * names always come from the fresh catalog and removed sections drop off.
 */
internal object AdminRecents {

    /** Fits two grid rows without pushing the catalog off the first screen. */
    const val MAX = 6

    private const val FILE = "vpsm_admin_recentes"
    private const val KEY = "ids"
    /** Unit separator (0x1F), which never occurs in a section id (`group.name`). */
    private const val SEPARATOR = "\u001F"

    fun read(context: Context): List<String> =
        prefs(context).getString(KEY, null)
            ?.split(SEPARATOR)
            ?.filter { it.isNotBlank() }
            ?.take(MAX)
            .orEmpty()

    /** Moves [sectionId] to the front without duplicates, capped at [MAX]. */
    fun registrar(context: Context, sectionId: String) {
        if (sectionId.isBlank()) return
        val next = (listOf(sectionId) + read(context).filterNot { it == sectionId }).take(MAX)
        prefs(context).edit().putString(KEY, next.joinToString(SEPARATOR)).apply()
    }

    private fun prefs(context: Context) =
        context.applicationContext.getSharedPreferences(FILE, Context.MODE_PRIVATE)
}
