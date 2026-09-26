package com.vpsmanager.data.update

import android.content.Context
import android.content.SharedPreferences

/**
 * Remembers the screen a person was on when they asked for the update, so that
 * the update does not drop them back at the start of the application.
 *
 * ## Why this has to exist
 *
 * Updating an application **kills its process**. That is not our choice:
 * Android replaces the package and tears down whatever was running. Someone
 * who tapped "Update" while looking at the Deploys page comes back on Home,
 * and has to walk the path again — a small thing when it happens once, an
 * irritation when it happens on every release.
 *
 * ## Why SharedPreferences with `commit`, and not DataStore
 *
 * This is the one place in the application where the write has to finish
 * BEFORE the next line: right after it the process can die at any instant,
 * because that very line is what authorised the installation. `DataStore`
 * writes asynchronously and is the right choice anywhere else; here it would
 * lose the race on the one occasion that matters.
 *
 * ## Why the route expires
 *
 * The route is only good for the restart caused by THIS update. With no
 * deadline, an update that failed would leave the route on disk, and the next
 * time the application opened — days later, for some other reason — it would
 * land on a screen nobody asked for. [consumir] returns `null` once the
 * deadline has passed, and clears it.
 */
class OndeEuEstava(
    context: Context,
    private val agora: () -> Long = System::currentTimeMillis,
) {

    private val prefs: SharedPreferences =
        context.applicationContext.getSharedPreferences(ARQUIVO, Context.MODE_PRIVATE)

    /**
     * Writes the route. Blocking on purpose — see the class KDoc.
     *
     * A null or blank [rota] erases instead of writing empty: "I do not know
     * where the person was" and "they were nowhere" lead to the same decision,
     * and a stored empty route would be a meaningless third possibility.
     */
    fun lembrar(rota: String?) {
        val limpa = rota?.trim().orEmpty()
        if (limpa.isEmpty()) {
            esquecer()
            return
        }
        prefs.edit()
            .putString(CHAVE_ROTA, limpa)
            .putLong(CHAVE_QUANDO, agora())
            .commit()
    }

    /**
     * Returns the route ONCE and erases it.
     *
     * Erasing on read is what stops the route from reappearing on some future
     * opening: it describes a return, not a preference.
     */
    fun consumir(): String? {
        val rota = prefs.getString(CHAVE_ROTA, null) ?: return null
        val quando = prefs.getLong(CHAVE_QUANDO, 0L)
        esquecer()
        if (agora() - quando > VALIDADE_MS) return null
        return rota
    }

    fun esquecer() {
        prefs.edit().remove(CHAVE_ROTA).remove(CHAVE_QUANDO).commit()
    }

    private companion object {
        const val ARQUIVO = "onde_eu_estava"
        const val CHAVE_ROTA = "rota"
        const val CHAVE_QUANDO = "quando"

        /**
         * Ten minutes. An update goes from tap to restart in seconds; ten
         * minutes comfortably cover a slow device or an installation the system
         * postponed, and are still short enough that nobody lands back on this
         * screen without having asked for it.
         */
        const val VALIDADE_MS = 10 * 60 * 1000L
    }
}
