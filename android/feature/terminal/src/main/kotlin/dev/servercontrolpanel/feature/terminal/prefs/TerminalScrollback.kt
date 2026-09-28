package dev.servercontrolpanel.feature.terminal.prefs

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.intPreferencesKey
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

enum class TerminalScrollback(val label: String, val lines: Int, val approxCost: String) {

    FIVE_THOUSAND("5k", 5_000, "~3 MB"),

    TEN_THOUSAND("10k", 10_000, "~5 MB"),

    TWENTY_THOUSAND("20k", 20_000, "~11 MB"),

    FIFTY_THOUSAND("50k", 50_000, "~27 MB"),
    ;

    val logBytesToFetch: Int
        get() = (lines.toLong() * LOG_BYTES_PER_LINE).coerceAtMost(FETCH_CAP_BYTES).toInt()

    companion object {
        val DEFAULT: TerminalScrollback = FIVE_THOUSAND

        private const val LOG_BYTES_PER_LINE = 1_300L

        private const val FETCH_CAP_BYTES = 16L * 1024 * 1024

        fun byName(name: String?): TerminalScrollback =
            entries.firstOrNull { it.name == name } ?: DEFAULT

        fun byRows(lines: Int): TerminalScrollback =
            entries.firstOrNull { it.lines == lines }
                ?: entries.lastOrNull { it.lines <= lines }
                ?: entries.first()
    }
}

class TerminalScrollbackPreference(
    context: Context,
    private val dataStore: DataStore<Preferences> = context.terminalPrefsDataStore,
) {

    val lines: Flow<Int> = dataStore.data.map { prefs ->
        prefs[SCROLLBACK_KEY] ?: TerminalScrollback.DEFAULT.lines
    }

    suspend fun setRows(value: Int) {
        dataStore.edit { prefs -> prefs[SCROLLBACK_KEY] = value }
    }

    companion object {
        private val SCROLLBACK_KEY = intPreferencesKey("terminal_scrollback_lines")
    }
}
