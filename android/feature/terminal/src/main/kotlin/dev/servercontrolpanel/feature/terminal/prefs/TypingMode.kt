package dev.servercontrolpanel.feature.terminal.prefs

import android.content.Context
import android.text.InputType
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

/**
 * How the device keyboard talks to the terminal.
 *
 * A terminal needs every key immediately (the remote program echoes, `Tab` and
 * `Ctrl+C` act now), while an autocorrector holds the whole word before deciding.
 * Declaring a text field while the `InputConnection` reports no text makes
 * keyboards correct against nothing and resend words as key events, so each mode
 * must be consistent end to end.
 *
 * [TERMINAL] is the default, as in Termux and ConnectBot (`TYPE_NULL`). [TEXT]
 * exists because the terminal is also where prose is written to the agent; the
 * pending word stays visible in `CompositionStrip`.
 */
enum class TypingMode(
    val label: String,
    val description: String,
    /** Value written to DataStore; kept stable so a saved choice survives renames. */
    val storedName: String,
) {

    /**
     * Every key goes straight to the terminal, no composition or corrector.
     * `TYPE_NULL` switches composition off at the source.
     */
    TERMINAL(
        label = "Terminal",
        description = "Every key arrives immediately. No autocorrect — which is what shell commands need.",
        storedName = "TERMINAL",
    ) {
        override fun inputType(): Int = InputType.TYPE_NULL
    },

    /**
     * The keyboard composes, corrects and suggests; the terminal receives the word
     * once confirmed. The `InputConnection` keeps the composing text and returns it
     * from `getTextBeforeCursor` and friends, so the corrector has real context.
     *
     * `TYPE_TEXT_FLAG_CAP_SENTENCES` suits prose; `TYPE_TEXT_FLAG_AUTO_COMPLETE` is
     * left out because it expects an app-provided candidate list.
     */
    TEXT(
        label = "Text",
        description = "Autocorrect and suggestions from your keyboard. The word being composed appears above the keys.",
        storedName = "TEXT",
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

    /** Whether this mode asks the `InputConnection` to keep composing text. */
    val composesText: Boolean get() = this == TEXT

    companion object {
        val DEFAULT: TypingMode = TERMINAL

        fun byName(name: String?): TypingMode =
            entries.firstOrNull { it.storedName == name } ?: DEFAULT
    }
}

/**
 * Persists the typing mode on the device only (a per-client keyboard choice, never
 * sent to the server). Stored by constant name so it survives changes to each
 * mode's `inputType`.
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
        private val MODE_KEY = stringPreferencesKey("terminal_typing_mode")
    }
}
