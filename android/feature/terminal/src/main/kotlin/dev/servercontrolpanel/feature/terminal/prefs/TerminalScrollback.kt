package dev.servercontrolpanel.feature.terminal.prefs

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.intPreferencesKey
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

/**
 * How many lines of history the user can scroll back through. It sets both the
 * emulator capacity and how much log is fetched to fill it on attach
 * ([logBytesToFetch]), since a fresh attach starts with an empty grid (`dtach`
 * keeps no screen).
 *
 * A ladder rather than a free field gives anchors for the memory and data cost,
 * as in Termux and iTerm2. The costs are worst-case ceilings: a snapshot cell is 8
 * bytes (`ghostty_jni.cpp`), about 536 B per 67-column line, while libghostty-vt
 * compresses scrollback per page.
 */
enum class TerminalScrollback(val label: String, val lines: Int, val approxCost: String) {

    FIVE_THOUSAND("5k", 5_000, "~3 MB"),

    TEN_THOUSAND("10k", 10_000, "~5 MB"),

    TWENTY_THOUSAND("20k", 20_000, "~11 MB"),

    FIFTY_THOUSAND("50k", 50_000, "~27 MB"),
    ;

    /**
     * How many bytes of raw log to fetch to fill [lines].
     *
     * The ratio is measured: replaying real logs onto a 67x40 grid gave 244 B per
     * line for a shell and up to 1,250 B for a repainting TUI. The worst case is
     * used so heavy sessions get the promised length; extra lines from light ones
     * just fall off the top.
     *
     * Capped at 16 MiB, the largest the server log can be (two 8 MiB generations,
     * `maxSessionLogBytes` in `internal/pty/sessionlog.go`). Transport gzip shrinks
     * it about 15x, and it is fetched once per attach.
     */
    val logBytesToFetch: Int
        get() = (lines.toLong() * LOG_BYTES_PER_LINE).coerceAtMost(FETCH_CAP_BYTES).toInt()

    companion object {
        val DEFAULT: TerminalScrollback = FIVE_THOUSAND

        /** See [logBytesToFetch]: worst case measured on real logs. */
        private const val LOG_BYTES_PER_LINE = 1_300L

        /** Two 8 MiB generations: the whole log, never more than exists. */
        private const val FETCH_CAP_BYTES = 16L * 1024 * 1024

        fun byName(name: String?): TerminalScrollback =
            entries.firstOrNull { it.name == name } ?: DEFAULT

        /**
         * The rung a stored value belongs to: an exact match, else the largest rung
         * not above it, else the smallest. Snapping instead of falling back to the
         * default keeps a choice from an older ladder visible in the options sheet.
         */
        fun byRows(lines: Int): TerminalScrollback =
            entries.firstOrNull { it.lines == lines }
                ?: entries.lastOrNull { it.lines <= lines }
                ?: entries.first()
    }
}

/**
 * Persists the history size on the device only (a per-client presentation choice).
 * Stored as the line count, since here the value is the meaning.
 *
 * The size is fixed when the emulator is created (`ghostty_terminal_new`) and the
 * history is fetched once on attach, so a change only applies to the next attached
 * session; the options sheet says so.
 */
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
        private val SCROLLBACK_KEY = intPreferencesKey("terminal_scrollback_linhas")
    }
}
