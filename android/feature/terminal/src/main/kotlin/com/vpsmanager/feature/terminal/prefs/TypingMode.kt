package com.vpsmanager.feature.terminal.prefs

import android.content.Context
import android.text.InputType
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

/**
 * How the device's keyboard talks to the terminal.
 *
 * ## The defect this fixes
 *
 * The app declared `TYPE_CLASS_TEXT or TYPE_TEXT_FLAG_MULTI_LINE` — an
 * ordinary text field, which INVITES the keyboard to compose words and
 * autocorrect them — while the `InputConnection` behind it was built with
 * `fullEditor = false` and answered EVERY getter with "there is no text
 * here": `getTextBeforeCursor` returned `""`, `getExtractedText` returned
 * `null`, `getSelectedText` returned `null`.
 *
 * Both sides could not be right at the same time. The keyboard's corrector
 * decides on a correction from the text around the cursor; given `""` it
 * corrects against nothing, and that is where the wrong substitutions came
 * from. Worse: some keyboards, on seeing that the editor "did not keep" what
 * was committed, resend the word as synthetic key events — which is why
 * `TerminalInputConnection` carried a 150 ms CLOCK-based tie-break that
 * swallowed keys matching whatever had just been committed. That tie-break
 * was the symptom, not the disease: it existed to paper over a contradiction
 * that this file removes.
 *
 * ## Why two modes, and not a single way
 *
 * A terminal and an autocorrector want incompatible things, and it is not a
 * matter of finding a better implementation:
 *
 * - A terminal needs EVERY key to arrive at once. What draws what you typed
 *   is the program on the other side, echoing byte by byte. `Tab` completes
 *   the word that has already arrived; `Ctrl+C` interrupts now.
 * - A corrector needs to hold the whole word back before deciding. While it
 *   holds it, the terminal has received nothing — and the screen sits still.
 *
 * That is why [TERMINAL] is the default, and it is what the field does:
 * Termux uses `InputType.TYPE_NULL` while the grid has focus (with a password
 * variant for Samsung keyboards, which ignore `TYPE_NULL`), and ConnectBot
 * does the same. With `TYPE_NULL` the keyboard stops composing and starts
 * sending key events — which is exactly what a terminal wants.
 *
 * And [TEXT] exists because in this app the terminal is also where PROSE is
 * written: it is through the terminal that one talks to the agent. There the
 * device's corrector is worth more than the immediate echo — provided the
 * pending composition stays VISIBLE, which is what the composition strip
 * solves (`CompositionStrip`). Without that strip, text mode would mean
 * typing blind, which is how it used to be.
 */
enum class TypingMode(
    val label: String,
    val description: String,
    /** Value written to DataStore; kept stable so a saved choice survives renames. */
    val storedName: String,
) {

    /**
     * Every key goes straight to the terminal, with no composition and no
     * corrector.
     *
     * `TYPE_NULL` is what Termux calls "the most correct input type" — it
     * switches composition off at the source, so there is no word held captive
     * inside the keyboard, no correction against an empty context, and no
     * synthetic resend to be tie-broken.
     */
    TERMINAL(
        label = "Terminal",
        description = "Every key arrives immediately. No autocorrect — which is what shell commands need.",
        storedName = "TERMINAL",
    ) {
        override fun inputType(): Int = InputType.TYPE_NULL
    },

    /**
     * The keyboard composes the word, corrects and suggests; the terminal
     * receives it once the word is confirmed.
     *
     * Here the `InputConnection` HONOURS the contract this type promises: it
     * keeps the text under composition and hands it back from
     * `getTextBeforeCursor` and friends, so that the corrector can decide with
     * the real context in hand.
     *
     * `TYPE_TEXT_FLAG_CAP_SENTENCES` is included because the use is prose;
     * `TYPE_TEXT_FLAG_AUTO_COMPLETE` is not, since it is meant for a field
     * with a candidate list of the app's own and would leave the keyboard
     * waiting for suggestions that would never arrive.
     */
    TEXT(
        label = "Text",
        description = "Autocorrect and suggestions from your keyboard. The word being composed appears above the keys.",
        storedName = "TEXTO",
    ) {
        override fun inputType(): Int =
            InputType.TYPE_CLASS_TEXT or
                InputType.TYPE_TEXT_FLAG_MULTI_LINE or
                InputType.TYPE_TEXT_FLAG_AUTO_CORRECT or
                InputType.TYPE_TEXT_FLAG_CAP_SENTENCES
    },
    ;

    /** The `EditorInfo.inputType` this mode declares to the keyboard. */
    abstract fun inputType(): Int

    /** Does this mode ask the `InputConnection` to keep text under composition? */
    val composesText: Boolean get() = this == TEXT

    companion object {
        val DEFAULT: TypingMode = TERMINAL

        fun byName(name: String?): TypingMode =
            entries.firstOrNull { it.storedName == name } ?: DEFAULT
    }
}

/**
 * Persists the typing mode, on the device only, by the same route and for the
 * same reason as [TerminalLineSpacingPreference]: it is ONE client's
 * preference about how its own keyboard behaves, not session state, and so it
 * never goes up to the server.
 *
 * Stored by the NAME of the constant so that it survives a future change to
 * the `inputType` of each mode.
 */
class TypingModePreference(
    context: Context,
    private val dataStore: DataStore<Preferences> = context.terminalPrefsDataStore,
) {

    val mode: Flow<TypingMode> = dataStore.data.map { prefs ->
        TypingMode.byName(prefs[MODE_KEY])
    }

    suspend fun setMode(value: TypingMode) {
        dataStore.edit { prefs -> prefs[MODE_KEY] = value.storedName }
    }

    companion object {
        private val MODE_KEY = stringPreferencesKey("terminal_modo_digitacao")
    }
}
