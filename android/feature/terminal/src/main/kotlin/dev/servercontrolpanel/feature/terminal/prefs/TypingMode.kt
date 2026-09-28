package dev.servercontrolpanel.feature.terminal.prefs

import android.content.Context
import android.text.InputType
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

enum class TypingMode(
    val label: String,
    val description: String,
    val storedName: String,
) {

    TERMINAL(
        label = "Terminal",
        description = "Every key arrives immediately. No autocorrect — which is what shell commands need.",
        storedName = "TERMINAL",
    ) {
        override fun inputType(): Int = InputType.TYPE_NULL
    },

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

    abstract fun inputType(): Int

    val composesText: Boolean get() = this == TEXT

    companion object {
        val DEFAULT: TypingMode = TERMINAL

        fun byName(name: String?): TypingMode =
            entries.firstOrNull { it.storedName == name } ?: DEFAULT
    }
}

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
