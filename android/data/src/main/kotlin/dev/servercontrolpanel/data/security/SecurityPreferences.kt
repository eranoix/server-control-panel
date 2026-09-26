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

/**
 * The two defences that depend on the device rather than the server. Whoever
 * holds the unlocked phone has the owner's power over the server, since the app
 * authenticates itself on open.
 *
 * Both default to OFF on purpose:
 * - [lockOnOpen] enabled without consent could lock the owner out of their own
 *   tool when biometrics fail.
 * - [protectFromCapture] blacks out screenshots, recordings and the Recents card,
 *   which breaks sharing a screenshot of a problem.
 */
class SecurityPreferences(
    context: Context,
    private val dataStore: DataStore<Preferences> = context.securityPrefsDataStore,
) {

    /**
     * Require biometrics or the device PIN on every return to the foreground. The
     * PIN is allowed so a failing fingerprint sensor does not lock the user out.
     */
    val lockOnOpen: Flow<Boolean> =
        dataStore.data.map { it[LOCK_KEY] ?: false }

    /**
     * `FLAG_SECURE` on the window: no screenshots, no screen recording and no
     * Recents thumbnail. The thumbnail matters most: Android writes it to disk,
     * and it may show terminal output such as a config file.
     */
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
