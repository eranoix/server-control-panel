package dev.servercontrolpanel.data.auth

import android.content.Context
import dev.servercontrolpanel.data.config.EncryptedServerConfigStore
import dev.servercontrolpanel.data.config.ServerConfigRepository

object AppSession {

    @Volatile
    private var instance: SessionManager? = null

    private val lock = Any()

    fun install(manager: SessionManager): SessionManager = synchronized(lock) {
        instance = manager
        manager
    }

    fun get(context: Context): SessionManager {
        instance?.let { return it }
        return synchronized(lock) {
            instance ?: create(context.applicationContext).also { instance = it }
        }
    }

    fun reset() = synchronized(lock) {
        instance = null
    }

    private fun create(appContext: Context): SessionManager {
        val serverConfigRepository = ServerConfigRepository(EncryptedServerConfigStore(appContext))
        return SessionManager(
            tokenStore = KeystoreTokenStore(appContext),
            refresher = BffSessionRefresher(serverConfigRepository),
        )
    }
}
