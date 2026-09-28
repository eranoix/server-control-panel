package dev.servercontrolpanel.data.config

import android.content.Context
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import dev.servercontrolpanel.core.model.ServerConfig
import java.io.IOException
import java.security.GeneralSecurityException

private const val PREFS_FILE_NAME = "servercontrolpanel_server_config"

private const val FALLBACK_PREFS_FILE_NAME = "servercontrolpanel_server_config_fallback"
private const val KEY_BASE_URL = "base_url"
private const val KEY_ALLOW_INSECURE_HTTP = "allow_insecure_http"

interface ServerConfigStore {
    fun load(): ServerConfig?
    fun save(config: ServerConfig)
    fun clear()
}

class EncryptedServerConfigStore(context: Context) : ServerConfigStore {

    private val appContext = context.applicationContext

    private val prefs: SharedPreferences by lazy { createPrefs() }

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
