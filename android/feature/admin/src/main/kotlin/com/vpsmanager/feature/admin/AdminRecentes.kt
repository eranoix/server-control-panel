package com.vpsmanager.feature.admin

import android.content.Context

/**
 * The most recently opened sections, on this device.
 *
 * ## Why this exists
 *
 * The server offers 25 sections and the person uses four. Without recents, the
 * launcher treats all 25 as equally likely and charges a full read on every
 * opening — which is exactly the cost the launcher existed to remove.
 *
 * ## Why SharedPreferences, and not DataStore
 *
 * `:feature-admin` does not depend on DataStore, and pulling the whole
 * dependency in to keep six strings would be paying dearly for very little.
 * Access here is rare (one read on opening, one write on choosing) and tiny,
 * which is precisely the case where `SharedPreferences` is still the right
 * answer — its problem is synchronous I/O in volume, not its existence.
 *
 * ## Why ids only, and never labels
 *
 * A section's label and group belong to the server and can change between two
 * launches of the app. Keeping the label here would create a second source of
 * truth that ages silently: the list would show the old name of a renamed
 * section. Keeping only the id, the name always comes from the fresh catalogue
 * — and an id that has ceased to exist simply does not match and drops off the
 * list.
 */
internal object AdminRecentes {

    /**
     * Six: they fit two rows of the grid without pushing the rest of the
     * catalogue off the first screen, and they comfortably cover the handful
     * of sections a person actually repeats. Keeping twenty would be keeping
     * the whole catalogue in another order.
     */
    const val MAXIMO = 6

    private const val ARQUIVO = "vpsm_admin_recentes"
    private const val CHAVE = "ids"
    /** Unit separator (US, 0x1F) — never occurs in a section id, which is `grupo.nome`. */
    private const val SEPARADOR = "\u001F"

    fun ler(context: Context): List<String> =
        prefs(context).getString(CHAVE, null)
            ?.split(SEPARADOR)
            ?.filter { it.isNotBlank() }
            ?.take(MAXIMO)
            .orEmpty()

    /**
     * Puts [sectionId] at the front, removes the previous occurrence
     * (otherwise opening the same section twice would duplicate it) and cuts
     * at the ceiling.
     */
    fun registrar(context: Context, sectionId: String) {
        if (sectionId.isBlank()) return
        val nova = (listOf(sectionId) + ler(context).filterNot { it == sectionId }).take(MAXIMO)
        prefs(context).edit().putString(CHAVE, nova.joinToString(SEPARADOR)).apply()
    }

    private fun prefs(context: Context) =
        context.applicationContext.getSharedPreferences(ARQUIVO, Context.MODE_PRIVATE)
}
