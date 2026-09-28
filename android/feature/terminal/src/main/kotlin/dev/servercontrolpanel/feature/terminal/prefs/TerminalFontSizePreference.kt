package dev.servercontrolpanel.feature.terminal.prefs

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.floatPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

internal val Context.terminalPrefsDataStore: DataStore<Preferences> by preferencesDataStore(name = "terminal_prefs")

class TerminalFontSizePreference(
    context: Context,
    private val dataStore: DataStore<Preferences> = context.terminalPrefsDataStore,
) {

    val fontSizeSp: Flow<Float> = dataStore.data.map { prefs ->
        prefs[FONT_SIZE_SP_KEY] ?: DEFAULT_FONT_SIZE_SP
    }

    suspend fun setFontSizeSp(sizeSp: Float) {
        val clamped = sizeSp.coerceIn(MIN_FONT_SIZE_SP, MAX_FONT_SIZE_SP)
        dataStore.edit { prefs -> prefs[FONT_SIZE_SP_KEY] = clamped }
    }

    companion object {
        private val FONT_SIZE_SP_KEY = floatPreferencesKey("terminal_font_size_sp")

        const val DEFAULT_FONT_SIZE_SP = 16f

        const val MIN_FONT_SIZE_SP = 10f

        const val MAX_FONT_SIZE_SP = 28f

        const val FONT_SIZE_STEP_SP = 2f
    }
}
