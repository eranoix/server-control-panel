package com.vpsmanager.data.seguranca

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

internal val Context.segurancaPrefsDataStore: DataStore<Preferences> by
    preferencesDataStore(name = "seguranca_prefs")

/**
 * The two defences that depend on the DEVICE, and not on the server.
 *
 * ## Why they exist, and why both are born OFF
 *
 * This app is a shell with privilege on the server. Whoever is holding the
 * unlocked phone has the same power as the owner — the token is encrypted at
 * rest (Keystore), but the app authenticates itself on opening, which is
 * exactly what you want day to day and exactly what you do not want when the
 * device changes hands.
 *
 * Neither of the two turns itself on, and that is a decision, not an omission:
 *
 * - [bloqueioAoAbrir] switched on without warning could LOCK THE PERSON OUT of
 *   their own work tool — biometrics fail, a wet sensor, an injured finger. A
 *   defence that imposes itself without consent becomes the incident it was
 *   meant to prevent.
 * - [protegerContraCaptura] leaves the screen BLACK in screenshots, in screen
 *   recordings and in the recent apps card. That breaks the habit of
 *   photographing the screen to show someone a problem — which is how this app
 *   has been getting debugged.
 *
 * Whoever switches them on is the one who knows their own context. The job here
 * is to leave both a single tap away, with the reason written beside them.
 */
class PreferenciasDeSeguranca(
    context: Context,
    private val dataStore: DataStore<Preferences> = context.segurancaPrefsDataStore,
) {

    /**
     * Require biometrics (or the device PIN) on every return to the
     * foreground.
     *
     * The PIN is offered as an alternative on purpose: a biometrics-only lock
     * fails exactly for the person with a greasy finger or a wet screen, and
     * then the defence turns into an obstacle.
     */
    val bloqueioAoAbrir: Flow<Boolean> =
        dataStore.data.map { it[BLOQUEIO_KEY] ?: false }

    /**
     * `FLAG_SECURE` on the window: no screenshots, no screen recording, and no
     * thumbnail in Recents.
     *
     * The thumbnail is the one that matters most and gets thought about least:
     * Android writes it TO DISK as you leave the app, and it shows the last
     * screen — which here is a terminal that may be holding the output of a
     * `cat` on a configuration file.
     */
    val protegerContraCaptura: Flow<Boolean> =
        dataStore.data.map { it[CAPTURA_KEY] ?: false }

    suspend fun definirBloqueioAoAbrir(valor: Boolean) {
        dataStore.edit { it[BLOQUEIO_KEY] = valor }
    }

    suspend fun definirProtecaoContraCaptura(valor: Boolean) {
        dataStore.edit { it[CAPTURA_KEY] = valor }
    }

    private companion object {
        val BLOQUEIO_KEY = booleanPreferencesKey("seguranca_bloqueio_ao_abrir")
        val CAPTURA_KEY = booleanPreferencesKey("seguranca_proteger_captura")
    }
}
