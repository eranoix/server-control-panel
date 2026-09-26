package com.vpsmanager.data.config

import android.content.Context
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import com.vpsmanager.core.model.ServerConfig
import java.io.IOException
import java.security.GeneralSecurityException

private const val PREFS_FILE_NAME = "vpsmanager_server_config"

/** Separate file name for the unencrypted fallback (see [EncryptedServerConfigStore.fallbackPrefs]) so it never collides with an [EncryptedSharedPreferences]-formatted file of the same name. */
private const val FALLBACK_PREFS_FILE_NAME = "vpsmanager_server_config_fallback"
private const val KEY_BASE_URL = "base_url"
private const val KEY_ALLOW_INSECURE_HTTP = "allow_insecure_http"

/** Persists the single [ServerConfig] this app is configured against. */
interface ServerConfigStore {
    fun load(): ServerConfig?
    fun save(config: ServerConfig)
    fun clear()
}

/**
 * Backed by [EncryptedSharedPreferences] (Android Keystore, AES256_GCM) — the
 * same mechanism [com.vpsmanager.data.auth.KeystoreTokenStore] uses for the
 * session tokens. The server address can never be LESS durable or LESS
 * protected than the credentials that will be sent to it, so the two share the
 * same storage class.
 *
 * What the two do NOT share is the fallback: here, an unavailable Keystore
 * degrades to `SharedPreferences` in the clear (see [createPrefs]); there, it
 * degrades to memory and the session dies at the next boot. The difference is
 * deliberate and is explained in the KDoc of `KeystoreTokenStore` — do not
 * copy this fallback over there.
 */
class EncryptedServerConfigStore(context: Context) : ServerConfigStore {

    private val appContext = context.applicationContext

    private val prefs: SharedPreferences by lazy { createPrefs() }

    /**
     * `ServerConfigRepository`'s constructor calls [load] eagerly, which
     * means this runs the very first time it is read — inside `Application.onCreate`,
     * before any UI exists to show a recovery path. `MasterKey.Builder.build()`
     * and `EncryptedSharedPreferences.create` both touch the Android Keystore
     * and can throw [GeneralSecurityException]/[IOException] on a real device
     * (a corrupted keystore entry after an OS/ROM upgrade, a device without a
     * working TEE/StrongBox, or a truncated prefs file from a killed-mid-write
     * process) — letting either propagate here would crash the app on every
     * single future launch with no way for the operator to recover short of a
     * data wipe. Falling back to a plain, unencrypted store trades encryption
     * for the app being usable again; the only data this store holds is the
     * configured server's base URL and an insecure-HTTP opt-in flag, neither a
     * credential.
     */
    private fun createPrefs(): SharedPreferences = try {
        createEncryptedPrefs()
    } catch (e: GeneralSecurityException) {
        fallbackPrefs()
    } catch (e: IOException) {
        fallbackPrefs()
    }

    private fun createEncryptedPrefs(): SharedPreferences {
        val masterKey = MasterKey.Builder(appContext)
            .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
            .build()
        return EncryptedSharedPreferences.create(
            appContext,
            PREFS_FILE_NAME,
            masterKey,
            EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
            EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
        )
    }

    private fun fallbackPrefs(): SharedPreferences =
        appContext.getSharedPreferences(FALLBACK_PREFS_FILE_NAME, Context.MODE_PRIVATE)

    /**
     * `ServerConfigRepository`'s constructor calls this eagerly on the main
     * thread with no crash containment around it — a single corrupted value
     * (e.g. a stored blob that no longer decrypts after a Keystore key
     * rotation) throwing here must degrade to "not configured yet" rather
     * than crash the app on every launch, same reasoning as [createPrefs].
     */
    override fun load(): ServerConfig? = runCatching {
        val baseUrl = prefs.getString(KEY_BASE_URL, null) ?: return@runCatching null
        ServerConfig(
            baseUrl = baseUrl,
            allowInsecureHttp = prefs.getBoolean(KEY_ALLOW_INSECURE_HTTP, false),
        )
    }.getOrNull()

    override fun save(config: ServerConfig) {
        prefs.edit()
            .putString(KEY_BASE_URL, config.baseUrl)
            .putBoolean(KEY_ALLOW_INSECURE_HTTP, config.allowInsecureHttp)
            .apply()
    }

    override fun clear() {
        prefs.edit().clear().apply()
    }
}
