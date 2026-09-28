package dev.servercontrolpanel.data.update

import android.content.Context
import android.content.SharedPreferences

class ResumePoint(
    context: Context,
    private val now: () -> Long = System::currentTimeMillis,
) {

    private val prefs: SharedPreferences =
        context.applicationContext.getSharedPreferences(FILE, Context.MODE_PRIVATE)

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
        const val FILE = "resume_point"
        const val KEY_ROUTE = "route"
        const val KEY_WHEN = "when"

        const val VALIDITY_MS = 10 * 60 * 1000L
    }
}
