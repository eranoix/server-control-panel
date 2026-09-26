package com.vpsmanager.feature.terminal.prefs

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.intPreferencesKey
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

/**
 * How many LINES of history a person can SCROLL BACK through and read.
 *
 * ## What this number came to mean
 *
 * It once meant only "emulator capacity" — and in that role it delivered
 * nothing, because the capacity was always empty. A freshly attached session
 * is born with a blank grid: `dtach` keeps no screen, and the earlier history
 * lives in the server's log. Choosing 200,000 lines of capacity for an
 * emulator that received 40 lines of content is choosing the size of an empty
 * cupboard.
 *
 * The number now drives both halves, which were always one and the same
 * thing: the emulator's capacity AND how much log the app fetches from the
 * server to fill that capacity on attach ([logBytesToFetch]). It is the
 * question a person really asks — "how far up can I scroll and reread?".
 *
 * ## Why a ladder, and not a free-text field
 *
 * The cost is memory on the device and mobile data on the fetch, and nobody
 * can estimate the cost of "37,500 lines". A ladder gives anchors. It is what
 * Termux (`terminal-transcript-rows`, a ladder from 100 to 50,000) and iTerm2
 * do.
 *
 * The memory figures below are measured on this grid: one snapshot cell takes
 * 8 bytes (`ghostty_jni.cpp`), so a line of 67 columns costs ~536 B in the
 * worst case — but libghostty-vt keeps the scrollback compressed per page and
 * only pays for what was written. The numbers are a ceiling, not an average.
 */
enum class TerminalScrollback(val label: String, val lines: Int, val approxCost: String) {

    FIVE_THOUSAND("5k", 5_000, "~3 MB"),

    TEN_THOUSAND("10k", 10_000, "~5 MB"),

    TWENTY_THOUSAND("20k", 20_000, "~11 MB"),

    FIFTY_THOUSAND("50k", 50_000, "~27 MB"),
    ;

    /**
     * How many bytes of raw log to fetch from the server to fill [lines].
     *
     * ## The 1,300 ratio is measured, not estimated
     *
     * A byte of log is not a line of screen: a program that repaints writes
     * the same frame dozens of times, and the emulator paints every one of
     * them in the same place. Replaying the REAL logs of this machine onto a
     * 67×40 grid, the ratio between log bytes and rendered lines came out as:
     *
     * ```
     *   main.log       (shell)         244 B per line
     *   Servidor.log   (mixed)         400 B per line
     *   Vpsm.log       (mixed)         667 B per line
     *   Aplicativo.log (Claude Code) 1,250 B per line
     * ```
     *
     * A fivefold difference between the light end and the heavy one. An
     * average ratio would leave precisely the conversation session — the one
     * a person wants to reread — half as long as promised, which is the very
     * defect being fixed. So the choice is the WORST measured case, with room
     * to spare: the light session pulls in more log than it needs (and the
     * leftover lines simply fall off the top of the scrollback, at no cost in
     * memory), while the heavy one reaches the number promised.
     *
     * The 16 MiB ceiling is not a product decision: it is the largest the log
     * can ever be on the server (8 MiB per generation, two generations — see
     * `maxSessionLogBytes` in `internal/pty/sessionlog.go`). Asking for more
     * than that would be asking for history that does not exist. On the wire
     * the transport's gzip shrinks the log by ~15× (measured: 6.2 MB →
     * 388 KB), and the fetch happens once, on attach.
     */
    val logBytesToFetch: Int
        get() = (lines.toLong() * LOG_BYTES_PER_LINE).coerceAtMost(FETCH_CAP_BYTES).toInt()

    companion object {
        val DEFAULT: TerminalScrollback = FIVE_THOUSAND

        /** See [logBytesToFetch]: worst case measured on this machine's real logs. */
        private const val LOG_BYTES_PER_LINE = 1_300L

        /** Two generations of 8 MiB — the whole log, never more than exists. */
        private const val FETCH_CAP_BYTES = 16L * 1024 * 1024

        fun byName(name: String?): TerminalScrollback =
            entries.firstOrNull { it.name == name } ?: DEFAULT

        /**
         * The rung that a stored value belongs to.
         *
         * An exact match when one exists; if it does not, it SNAPS to the
         * largest rung that does not exceed the stored value (and to the
         * smallest, when the stored value is below them all). Falling back to
         * the default would be wrong here: the ladder has already changed
         * once, and anyone who had picked 200,000 lines would be left with an
         * options sheet with no rung lit at all — the screen saying that their
         * choice does not exist, instead of saying where it ended up.
         */
        fun byRows(lines: Int): TerminalScrollback =
            entries.firstOrNull { it.lines == lines }
                ?: entries.lastOrNull { it.lines <= lines }
                ?: entries.first()
    }
}

/**
 * Persists the size of the history, on the device only, for the same reason
 * as [TerminalLineSpacingPreference]: it is a presentation preference of ONE
 * client.
 *
 * Stored as an INTEGER (the number of lines) rather than by the name of the
 * constant, on purpose — unlike the line spacing, here the value **is** the
 * meaning: if the ladder ever changes its rungs, whoever picked 50,000 lines
 * still has 50,000, instead of being reallocated to the nearest rung.
 *
 * ⚠️ The scrollback size is fixed at the emulator's **creation**
 * (`ghostty_terminal_new`), and the history fetch happens once, on attach —
 * so changing it here only takes effect for the next attached session. The
 * options sheet says so on screen; hiding that condition would make a person
 * think the change had not worked.
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
