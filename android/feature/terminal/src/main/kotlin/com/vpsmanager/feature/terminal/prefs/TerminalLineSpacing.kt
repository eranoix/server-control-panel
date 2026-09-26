package com.vpsmanager.feature.terminal.prefs

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

/**
 * The line-spacing steps on offer, as a DELTA in whole pixels over the usual
 * cell height.
 *
 * **Why a pixel delta and not a multiplier.** A multiplier is what iTerm2
 * (0.5x to 2.0x) and WezTerm (`line_height`) expose, and it would be the
 * natural choice — but they run in desktop windows, with cells of 30 to 60 px.
 * Here a cell is ~42 px and can drop to 26 at the smallest text size; in that
 * range 0.90 and 0.95 round to the same integer, and a person sees two menu
 * items that do the same thing. A whole-pixel delta is exactly representable
 * at any font size and is what Alacritty exposes (`font.offset.y`, "extra
 * space around each character", added to the metric and truncated once).
 *
 * **Why the ladder is short.** How much room there is to take away was
 * MEASURED on the emulator: at a text size of 16 sp (a cell of 42 px) the
 * font's typographic box takes 40 px and the box-drawing ink (`█`, `│`) takes
 * 41. In other words, this terminal's grid is ALREADY tight — there was no
 * spare leading sitting there waiting to be cut. [COMPACTA] takes away what
 * real slack exists without clipping a single letter (the floor is computed
 * from the measured ink, in `TerminalCellMetrics`), and the honest gain is of
 * the order of 3 extra rows per screen, not 10.
 */
enum class TerminalLineSpacing(val rotulo: String, val deltaPx: Int) {

    /** The tightest that fits without clipping a letter. Wins a few rows per screen. */
    COMPACTA("Compact", -3),

    /** One pixel less: all but imperceptible, one row more. */
    JUSTA("Tight", -1),

    /** The usual grid. */
    NORMAL("Normal", 0),

    /** A little more breathing room between the rows, for long reading. */
    FOLGADA("Relaxed", 2),
    ;

    companion object {
        val PADRAO: TerminalLineSpacing = NORMAL

        fun porNome(nome: String?): TerminalLineSpacing =
            entries.firstOrNull { it.name == nome } ?: PADRAO
    }
}

/**
 * Persists the chosen line spacing, on the device only, by the same route and
 * for the same reason as [TerminalFontSizePreference]: it is ONE client's
 * presentation preference, not session state, and so it never goes up to the
 * server.
 *
 * Stored by the NAME of the constant, not by the delta: if the ladder ever
 * changes its values, whoever has already chosen "Compact" keeps the new
 * compact, instead of being stuck on a `-3` that no longer exists on the
 * ladder.
 *
 * [dataStore] is injectable for the same reason as in the font preference —
 * the global instance is cached per [Context] for the whole process, and a
 * test that wants isolated storage passes its own.
 */
class TerminalLineSpacingPreference(
    context: Context,
    private val dataStore: DataStore<Preferences> = context.terminalPrefsDataStore,
) {

    val entrelinha: Flow<TerminalLineSpacing> = dataStore.data.map { prefs ->
        TerminalLineSpacing.porNome(prefs[LINE_SPACING_KEY])
    }

    suspend fun setEntrelinha(valor: TerminalLineSpacing) {
        dataStore.edit { prefs -> prefs[LINE_SPACING_KEY] = valor.name }
    }

    companion object {
        private val LINE_SPACING_KEY = stringPreferencesKey("terminal_line_spacing")
    }
}
