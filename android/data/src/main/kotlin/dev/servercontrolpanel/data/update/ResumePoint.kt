package dev.servercontrolpanel.data.update

import android.content.Context
import android.content.SharedPreferences

/**
 * Remembers the screen the user was on when they asked for the update, since
 * installing kills the process and would otherwise drop them on Home.
 *
 * SharedPreferences with `commit` rather than DataStore: the write must finish
 * before the next line, because the process may die right after installation is
 * authorised. The route expires so a failed update does not land a later, unrelated
 * launch on that screen.
 */
class ResumePoint(
    context: Context,
    private val now: () -> Long = System::currentTimeMillis,
) {

    private val prefs: SharedPreferences =
        context.applicationContext.getSharedPreferences(FILE, Context.MODE_PRIVATE)

    /**
     * Writes the route synchronously (see the class KDoc). A null or blank
     * [route] erases the stored one instead of storing an empty value.
     */
    fun save(route: String?) {
        val trimmed = route?.trim().orEmpty()
        if (trimmed.isEmpty()) {
            forget()
            return
        }
        prefs.edit()
            .putString(KEY_ROUTE, trimmed)
            .putLong(KEY_WHEN, now())
            .commit()
    }

    /** Returns the route once and erases it: it describes a return, not a preference. */
    fun consume(): String? {
        val route = prefs.getString(KEY_ROUTE, null) ?: return null
        val whenText = prefs.getLong(KEY_WHEN, 0L)
        forget()
        if (now() - whenText > VALIDITY_MS) return null
        return route
    }

    fun forget() {
        prefs.edit().remove(KEY_ROUTE).remove(KEY_WHEN).commit()
    }

    private companion object {
        const val FILE = "onde_eu_estava"
        const val KEY_ROUTE = "rota"
        const val KEY_WHEN = "quando"

        /**
         * Ten minutes: covers a slow device or a postponed install, yet short
         * enough that nobody lands here without having asked.
         */
        const val VALIDITY_MS = 10 * 60 * 1000L
    }
}
