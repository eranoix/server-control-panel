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
 * land on a screen nobody asked for. [consume] returns `null` once the
 * deadline has passed, and clears it.
 */
class ResumePoint(
    context: Context,
    private val now: () -> Long = System::currentTimeMillis,
) {

    private val prefs: SharedPreferences =
        context.applicationContext.getSharedPreferences(FILE, Context.MODE_PRIVATE)

    /**
     * Writes the route. Blocking on purpose — see the class KDoc.
     *
     * A null or blank [route] erases instead of writing empty: "I do not know
     * where the person was" and "they were nowhere" lead to the same decision,
     * and a stored empty route would be a meaningless third possibility.
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

    /**
     * Returns the route ONCE and erases it.
     *
     * Erasing on read is what stops the route from reappearing on some future
     * opening: it describes a return, not a preference.
     */
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
         * Ten minutes. An update goes from tap to restart in seconds; ten
         * minutes comfortably cover a slow device or an installation the system
         * postponed, and are still short enough that nobody lands back on this
         * screen without having asked for it.
         */
        const val VALIDITY_MS = 10 * 60 * 1000L
    }
}
