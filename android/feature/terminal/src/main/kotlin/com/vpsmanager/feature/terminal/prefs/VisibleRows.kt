package com.vpsmanager.feature.terminal.prefs

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.intPreferencesKey
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

/**
 * How many terminal rows fit on screen. The user picks a row count (e.g. to see
 * all of `docker ps`) and the app derives the font size that fits it, like
 * "fit to page" in a PDF reader. [AUTOMATIC], the default, keeps the font size
 * in charge.
 */
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

/**
 * Persisted as the integer row count, not the enum name. A stored value no longer
 * in the list falls back to [VisibleRows.DEFAULT].
 */
class VisibleRowsPreference(
    context: Context,
    private val dataStore: DataStore<Preferences> = context.terminalPrefsDataStore,
) {
    val lines: Flow<Int> = dataStore.data.map { it[KEY] ?: VisibleRows.DEFAULT.lines }

    suspend fun setRows(value: Int) {
        dataStore.edit { it[KEY] = value }
    }

    private companion object {
        val KEY = intPreferencesKey("terminal_linhas_visiveis")
    }
}
