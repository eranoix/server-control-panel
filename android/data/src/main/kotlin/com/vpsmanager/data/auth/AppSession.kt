package com.vpsmanager.data.auth

import android.content.Context
import com.vpsmanager.data.config.EncryptedServerConfigStore
import com.vpsmanager.data.config.ServerConfigRepository

/**
 * The process's [SessionManager]. A session is by definition a single state:
 * two managers would mean two renewal queues fighting over the same rotating
 * refresh token (each would invalidate the other's token — see
 * [BffSessionRefresher]) and one screen reading "signed in" while another
 * reads "signed out".
 *
 * This app does not have a DI graph yet (see the KDoc of
 * `VpsManagerApplication`), and composables assemble their own dependencies at
 * the call site. An explicit singleton is the honest way to express "one per
 * process" in that arrangement — the same pattern
 * [com.vpsmanager.data.videocall.IncomingCallDispatcher] already uses for the
 * incoming call handler. Once a DI graph exists, this becomes a `@Singleton`
 * and [install] goes away.
 *
 * [install] is called by `VpsManagerApplication.onCreate` so that the manager
 * shares the SAME [ServerConfigRepository] as the rest of the app. [get] is
 * there for the call sites that only have a `Context` (composables) and builds
 * one on its own only if nobody installed one first — so that a test, or an
 * unexpected initialisation order, never ends up with two sessions.
 */
object AppSession {

    @Volatile
    private var instance: SessionManager? = null

    private val lock = Any()

    /** Registers the process's manager. Call once, in `Application.onCreate`. */
    fun install(manager: SessionManager): SessionManager = synchronized(lock) {
        instance = manager
        manager
    }

    /** The process's manager, building it from [context] if there is not one yet. */
    fun get(context: Context): SessionManager {
        instance?.let { return it }
        return synchronized(lock) {
            instance ?: create(context.applicationContext).also { instance = it }
        }
    }

    /** Tests only: undoes [install] so the next case starts clean. */
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
