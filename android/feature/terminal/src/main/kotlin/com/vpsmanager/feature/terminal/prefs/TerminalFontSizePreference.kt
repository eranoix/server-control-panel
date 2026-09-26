package com.vpsmanager.feature.terminal.prefs

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.floatPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

internal val Context.terminalPrefsDataStore: DataStore<Preferences> by preferencesDataStore(name = "terminal_prefs")

/**
 * Persists the terminal's font size purely on-device via Jetpack
 * DataStore — no network/server call, matching this codebase's existing
 * boundary that per-client UI preferences (unlike session state) never touch
 * the server. Stored as sp, the same unit [computeCellMetrics] in
 * `TerminalRoute` already converts to px, so the read path is a single unit
 * conversion, not a second source of truth for pixel sizing.
 *
 * [dataStore] defaults to the app-wide `by preferencesDataStore(...)`
 * singleton, but is an injectable seam: that singleton is cached per
 * [Context] instance for the process lifetime, so a test that wants a fresh,
 * isolated store (rather than one that can leak a value across `@Test`
 * methods sharing the same Robolectric application context) passes its own
 * temp-file-backed [DataStore] instead.
 */
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

        /** Matches `TERMINAL_LINE_HEIGHT_SP`, the line height `TerminalRoute` used before this preference existed. */
        const val DEFAULT_FONT_SIZE_SP = 16f

        /** Below this, glyphs become unreadable rather than merely small. */
        const val MIN_FONT_SIZE_SP = 10f

        /** Above this, too few columns/rows fit on a phone screen to be usable. */
        const val MAX_FONT_SIZE_SP = 28f

        /** How much one tap of the +/- control changes the size by. */
        const val FONT_SIZE_STEP_SP = 2f
    }
}
