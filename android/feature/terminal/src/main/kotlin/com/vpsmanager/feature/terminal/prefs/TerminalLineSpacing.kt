package com.vpsmanager.feature.terminal.prefs

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

/**
 * Line-spacing steps, as a whole-pixel DELTA over the usual cell height.
 *
 * A pixel delta rather than a multiplier (as in Alacritty's `font.offset.y`):
 * cells here are only 26 to 42 px, where multipliers like 0.90 and 0.95 round to
 * the same height. The ladder is short because the grid is already tight
 * (measured at 16 sp: 42 px cell, 41 px box-drawing ink); [COMPACT] removes the
 * real slack without clipping (the floor comes from `TerminalCellMetrics`),
 * gaining about 3 rows per screen.
 */
enum class TerminalLineSpacing(
    val label: String,
    val deltaPx: Int,
    /** Value written to DataStore; kept stable so a saved choice survives renames. */
    val storedName: String,
) {

    /** The tightest that fits without clipping a letter; gains a few rows per screen. */
    COMPACT("Compact", -3, "COMPACTA"),

    /** One pixel less: barely visible, one more row. */
    TIGHT("Tight", -1, "JUSTA"),

    /** The usual grid. */
    NORMAL("Normal", 0, "NORMAL"),

    /** A little more room between rows, for long reading. */
    RELAXED("Relaxed", 2, "FOLGADA"),
    ;

    companion object {
        val DEFAULT: TerminalLineSpacing = NORMAL

        fun byName(name: String?): TerminalLineSpacing =
            entries.firstOrNull { it.storedName == name } ?: DEFAULT
    }
}

/**
 * Persists the chosen line spacing on the device only (a per-client presentation
 * choice, like [TerminalFontSizePreference]). Stored by constant name, not delta,
 * so a saved choice follows the ladder if its values change. [dataStore] is
 * injectable so tests get isolated storage.
 */
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
