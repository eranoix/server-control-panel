package dev.servercontrolpanel.data.security

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

internal val Context.securityPrefsDataStore: DataStore<Preferences> by
    preferencesDataStore(name = "security_prefs")

class SecurityPreferences(
    context: Context,
    private val dataStore: DataStore<Preferences> = context.securityPrefsDataStore,
) {

    val lockOnOpen: Flow<Boolean> =
        dataStore.data.map { it[LOCK_KEY] ?: false }

    val protectFromCapture: Flow<Boolean> =
        dataStore.data.map { it[CAPTURE_KEY] ?: false }

    suspend fun setLockOnOpen(value: Boolean) {
        dataStore.edit { it[LOCK_KEY] = value }
    }

    suspend fun setProtectFromCapture(value: Boolean) {
        dataStore.edit { it[CAPTURE_KEY] = value }
    }

    private companion object {
        val LOCK_KEY = booleanPreferencesKey("security_lock_on_open")
        val CAPTURE_KEY = booleanPreferencesKey("security_block_capture")
    }
}
