package dev.servercontrolpanel.designsystem

import android.content.Context
import android.content.SharedPreferences
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * The appearance chosen by the device owner, persisted locally.
 *
 * Uses SharedPreferences rather than DataStore because the theme must be known
 * synchronously before the first frame; an async read would flash the default
 * theme first. The [StateFlow]'s initial value is already the stored one.
 *
 * [prefs] is injectable for tests, since the [get] singleton would leak state between tests.
 */
class ThemePreference(
    private val prefs: SharedPreferences,
) {

    constructor(context: Context) : this(
        context.applicationContext.getSharedPreferences(FILE_NAME, Context.MODE_PRIVATE),
    )

    private val _mode = MutableStateFlow(ThemeMode.byId(prefs.getString(KEY_MODE, null)))

    /** The current appearance. The value is already right before the first composition. */
    val mode: StateFlow<ThemeMode> = _mode.asStateFlow()

    /** Synchronous read, for whoever needs the value outside a composition. */
    fun current(): ThemeMode = _mode.value

    /**
     * Persists and publishes the choice. `apply()` is enough: the [StateFlow] is the
     * in-memory source of truth, so waiting for the disk would only stall the tap.
     */
    fun set(mode: ThemeMode) {
        prefs.edit().putString(KEY_MODE, mode.id).apply()
        _mode.value = mode
    }

    companion object {
        private const val FILE_NAME = "panel_appearance"
        private const val KEY_MODE = "theme_mode"

        @Volatile
        private var instance: ThemePreference? = null

        /** Process-wide instance, so every Activity sees the same value and changes. */
        fun get(context: Context): ThemePreference =
            instance ?: synchronized(this) {
                instance ?: ThemePreference(context).also { instance = it }
            }
    }
}
