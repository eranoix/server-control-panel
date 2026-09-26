package com.vpsmanager.feature.terminal.prefs

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.intPreferencesKey
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

/**
 * How many terminal rows fit on screen.
 *
 * ## Why this exists, when there is already a font size and a line height
 *
 * Because the question people ask is the other one. Nobody opens the terminal on
 * their phone thinking "I want 13 sp type": they think "I want to see the whole
 * output of `docker ps`", which is a count of ROWS. With only a font size,
 * arriving at a number of rows is trial and error — change the size, count the
 * rows, change it again.
 *
 * Here the direction is reversed: you choose the number of rows and the app
 * derives the type size that makes exactly that fit. It is the same move as
 * "fit to page" in a PDF reader.
 *
 * ## [AUTOMATIC] is not "no value"
 *
 * It is the old behaviour, and it remains the default: the font size rules and
 * the number of rows follows from it. Anyone who likes choosing the type loses
 * nothing; anyone who wants to count rows gains direct control.
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
 * Persisted as an INTEGER, and not by the enum name: here the number IS the
 * meaning. A stored value that stops existing in the list falls back to
 * [VisibleRows.DEFAULT] instead of bringing the screen down.
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
