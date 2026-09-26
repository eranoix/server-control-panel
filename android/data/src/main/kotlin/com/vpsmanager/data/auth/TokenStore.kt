package com.vpsmanager.data.auth

import android.content.Context
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import java.io.IOException
import java.security.GeneralSecurityException

private const val TOKEN_PREFS_FILE_NAME = "vpsmanager_session_tokens"
private const val KEY_ACCESS_TOKEN = "access_token"
private const val KEY_REFRESH_TOKEN = "refresh_token"
private const val KEY_EXPIRES_AT = "expires_at"

/**
 * A mobile session's credential pair, exactly as the BFF issues it
 * (`mobileLoginOutput`/`mobileRefreshOutput`/`passkeyLoginFinishOutput` in
 * `internal/mobilebff`): a short-lived access token (the session JWT) and a
 * rotating refresh token.
 *
 * [expiresAtEpochMillis] is the server's `expires_in` already converted into an
 * absolute instant on this device's clock. Storing the instant, and not the
 * duration, is what makes it possible to decide "is it expiring?" after the
 * process has been killed and reopened — a relative duration would lose its
 * meaning the moment its origin died with the process.
 */
data class SessionTokens(
    val accessToken: String,
    val refreshToken: String,
    val expiresAtEpochMillis: Long,
)

/**
 * Holds the session tokens. Unlike
 * [com.vpsmanager.data.config.ServerConfigStore], which degrades to plaintext
 * `SharedPreferences` when the Keystore fails, this store FAILS CLOSED — see
 * [KeystoreTokenStore].
 */
interface TokenStore {

    fun load(): SessionTokens?

    fun save(tokens: SessionTokens)

    fun clear()

    /**
     * `false` when the store has degraded to memory: the session dies with the
     * process and the operator will have to log in again on the next boot.
     * Exposed so the UI can WARN the operator, instead of their discovering on
     * their own that the app "forgets" the session every time.
     */
    val isPersistent: Boolean
}

/**
 * Keeps them in memory only. It is [KeystoreTokenStore]'s fallback destination
 * and also what the tests use.
 */
class InMemoryTokenStore(initial: SessionTokens? = null) : TokenStore {

    @Volatile
    private var tokens: SessionTokens? = initial

    override fun load(): SessionTokens? = tokens

    override fun save(tokens: SessionTokens) {
        this.tokens = tokens
    }

    override fun clear() {
        tokens = null
    }

    override val isPersistent: Boolean = false
}

/**
 * Stores the tokens in [EncryptedSharedPreferences] (Android Keystore,
 * AES256_GCM) and, if the Keystore is unavailable, IN MEMORY ONLY.
 *
 * A WARNING TO ANYONE EDITING THIS: the temptation to copy the neighbour's
 * fallback ([com.vpsmanager.data.config.EncryptedServerConfigStore], which on
 * failure falls back to PLAINTEXT `SharedPreferences`) is real, and it would be
 * a security failure. The two cases are deliberately different:
 *
 * - `ServerConfigStore` holds a server address and an insecure-HTTP flag. None
 *   of that is a credential: writing it in the clear costs almost no privacy
 *   and buys an app that opens again after a Keystore corrupted by a ROM
 *   upgrade.
 * - THIS store holds the session JWT and the refresh token. A plaintext
 *   `SharedPreferences` is an XML file in the app's directory — readable by any
 *   process with root, by any ADB backup, and by any backup restored onto
 *   ANOTHER device. Trading "the operator logs in again" for "the token leaks
 *   to disk" is a terrible deal: the cost of failing closed is one extra login
 *   per boot; the cost of failing open is the panel's whole session in the
 *   hands of whoever reads the file.
 *
 * Hence: if the Keystore fails, the session lives in memory only and is gone on
 * the next boot. NEVER write an unencrypted token to disk.
 *
 * Building the prefs sits in a `by lazy` with the try/catch INSIDE the
 * initialiser (not around it) for the same reason documented in
 * [com.vpsmanager.data.config.EncryptedServerConfigStore]: `by lazy` does not
 * memoise an exception, so an initialiser that throws would throw again on
 * every subsequent access.
 */
class KeystoreTokenStore(context: Context) : TokenStore {

    private val appContext = context.applicationContext

    /** Null when the Keystore is unavailable — see [memoryFallback]. */
    private val prefs: SharedPreferences? by lazy { createEncryptedPrefsOrNull() }

    /**
     * Only used when [prefs] is null. Always instantiated (it is cheap and has
     * no side effects) so [save]/[load]/[clear] need no second conditional
     * initialisation path.
     */
    private val memoryFallback = InMemoryTokenStore()

    private fun createEncryptedPrefsOrNull(): SharedPreferences? = try {
        val masterKey = MasterKey.Builder(appContext)
            .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
            .build()
        EncryptedSharedPreferences.create(
            appContext,
            TOKEN_PREFS_FILE_NAME,
            masterKey,
            EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
            EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
        )
    } catch (e: GeneralSecurityException) {
        null
    } catch (e: IOException) {
        null
    }

    override val isPersistent: Boolean
        get() = prefs != null

    override fun load(): SessionTokens? {
        val store = prefs ?: return memoryFallback.load()
        // A blob that no longer decrypts (a Keystore key rotation) has to
        // become "no session", never a crash at boot — the same reasoning as
        // EncryptedServerConfigStore.
        return runCatching {
            val access = store.getString(KEY_ACCESS_TOKEN, null) ?: return@runCatching null
            val refresh = store.getString(KEY_REFRESH_TOKEN, null) ?: return@runCatching null
            SessionTokens(
                accessToken = access,
                refreshToken = refresh,
                expiresAtEpochMillis = store.getLong(KEY_EXPIRES_AT, 0L),
            )
        }.getOrNull()
    }

    override fun save(tokens: SessionTokens) {
        val store = prefs
        if (store == null) {
            memoryFallback.save(tokens)
            return
        }
        store.edit()
            .putString(KEY_ACCESS_TOKEN, tokens.accessToken)
            .putString(KEY_REFRESH_TOKEN, tokens.refreshToken)
            .putLong(KEY_EXPIRES_AT, tokens.expiresAtEpochMillis)
            .apply()
    }

    override fun clear() {
        val store = prefs
        if (store == null) {
            memoryFallback.clear()
            return
        }
        store.edit().clear().apply()
    }
}
