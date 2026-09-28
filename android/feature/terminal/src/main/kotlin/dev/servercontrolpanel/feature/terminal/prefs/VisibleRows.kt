package dev.servercontrolpanel.feature.terminal.prefs

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.intPreferencesKey
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

enum class VisibleRows(val lines: Int, val label: String) {
    AUTOMATIC(0, "auto"),
    TWENTY_FOUR(24, "24"),
    THIRTY(30, "30"),
    THIRTY_SIX(36, "36"),
    FORTY_FIVE(45, "45"),
    SIXTY(60, "60"),
    ;

    companion object {
        val DEFAULT: VisibleRows = AUTOMATIC

        fun byRows(lines: Int): VisibleRows =
            entries.firstOrNull { it.lines == lines } ?: DEFAULT
    }
}

class VisibleRowsPreference(
    context: Context,
    private val dataStore: DataStore<Preferences> = context.terminalPrefsDataStore,
) {
    val lines: Flow<Int> = dataStore.data.map { it[KEY] ?: VisibleRows.DEFAULT.lines }

    suspend fun setRows(value: Int) {
        dataStore.edit { it[KEY] = value }
    }

    private companion object {
        val KEY = intPreferencesKey("terminal_visible_rows")
    }
}
