package dev.servercontrolpanel.data.auth

import dev.servercontrolpanel.data.config.ServerConfigRepository
import dev.servercontrolpanel.data.offline.ReadCache
import dev.servercontrolpanel.mobileapiclient.api.AuthApi

interface SignOutSource {
    suspend fun signOut()
}

class SignOutRepository(
    private val session: SessionManager,
    private val serverConfigRepository: ServerConfigRepository,
    private val authApiFactory: (String) -> AuthApi = { basePath -> AuthApi(basePath) },
) : SignOutSource {

    override suspend fun signOut() {
        try {
            val basePath = serverConfigRepository.currentBaseUrl()?.let { "$it/api/mobile/v1" }
            if (basePath != null) {
                authApiFactory(basePath).mobileLogout()
            }
        } catch (e: Exception) {
        } finally {
            session.signOut()
            ReadCache.clear()
        }
    }
}
