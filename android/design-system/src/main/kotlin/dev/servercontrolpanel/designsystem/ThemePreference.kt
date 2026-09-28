package dev.servercontrolpanel.designsystem

import android.content.Context
import android.content.SharedPreferences
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

class ThemePreference(
    private val prefs: SharedPreferences,
) {

    constructor(context: Context) : this(
        context.applicationContext.getSharedPreferences(FILE_NAME, Context.MODE_PRIVATE),
    )

    private val _mode = MutableStateFlow(ThemeMode.byId(prefs.getString(KEY_MODE, null)))

    val mode: StateFlow<ThemeMode> = _mode.asStateFlow()

    fun current(): ThemeMode = _mode.value

    fun set(mode: ThemeMode) {
        prefs.edit().putString(KEY_MODE, mode.id).apply()
        _mode.value = mode
    }

    companion object {
        private const val FILE_NAME = "panel_appearance"
        private const val KEY_MODE = "theme_mode"

        @Volatile
        private var instance: ThemePreference? = null

        fun get(context: Context): ThemePreference =
            instance ?: synchronized(this) {
                instance ?: ThemePreference(context).also { instance = it }
            }
    }
}
