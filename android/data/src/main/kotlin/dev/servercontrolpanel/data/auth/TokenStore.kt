package dev.servercontrolpanel.data.auth

import android.content.Context
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import java.io.IOException
import java.security.GeneralSecurityException

private const val TOKEN_PREFS_FILE_NAME = "servercontrolpanel_session_tokens"
private const val KEY_ACCESS_TOKEN = "access_token"
private const val KEY_REFRESH_TOKEN = "refresh_token"
private const val KEY_EXPIRES_AT = "expires_at"

data class SessionTokens(
    val accessToken: String,
    val refreshToken: String,
    val expiresAtEpochMillis: Long,
)

interface TokenStore {

    fun load(): SessionTokens?

    fun save(tokens: SessionTokens)

    fun clear()

    val isPersistent: Boolean
}

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

class KeystoreTokenStore(context: Context) : TokenStore {

    private val appContext = context.applicationContext

    private val prefs: SharedPreferences? by lazy { createEncryptedPrefsOrNull() }

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
