package dev.servercontrolpanel.feature.terminal.prefs

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

enum class TerminalLineSpacing(
    val label: String,
    val deltaPx: Int,
    val storedName: String,
) {

    COMPACT("Compact", -3, "COMPACT"),

    TIGHT("Tight", -1, "TIGHT"),

    NORMAL("Normal", 0, "NORMAL"),

    RELAXED("Relaxed", 2, "RELAXED"),
    ;

    companion object {
        val DEFAULT: TerminalLineSpacing = NORMAL

        fun byName(name: String?): TerminalLineSpacing =
            entries.firstOrNull { it.storedName == name } ?: DEFAULT
    }
}

class TerminalLineSpacingPreference(
    context: Context,
    private val dataStore: DataStore<Preferences> = context.terminalPrefsDataStore,
) {

    val lineSpacing: Flow<TerminalLineSpacing> = dataStore.data.map { prefs ->
        TerminalLineSpacing.byName(prefs[LINE_SPACING_KEY])
    }

    suspend fun setLineSpacing(value: TerminalLineSpacing) {
        dataStore.edit { prefs -> prefs[LINE_SPACING_KEY] = value.storedName }
    }

    companion object {
        private val LINE_SPACING_KEY = stringPreferencesKey("terminal_line_spacing")
    }
}
