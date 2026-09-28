package dev.servercontrolpanel.data.auth

import dev.servercontrolpanel.data.config.ServerConfigRepository
import dev.servercontrolpanel.mobileapiclient.api.AuthApi
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import dev.servercontrolpanel.mobileapiclient.model.MobileRefreshInputBody
import java.io.IOException

private const val HTTP_UNAUTHORIZED = 401
private const val HTTP_FORBIDDEN = 403

class BffSessionRefresher(
    private val serverConfigRepository: ServerConfigRepository,
    private val authApiFactory: (String) -> AuthApi = { basePath -> AuthApi(basePath) },
) : SessionRefresher {

    override suspend fun refresh(refreshToken: String): RefreshOutcome {
        val basePath = serverConfigRepository.currentBaseUrl()?.let { "$it/api/mobile/v1" }
            ?: return RefreshOutcome.Rejected
        return try {
            val response = authApiFactory(basePath).mobileRefresh(
                MobileRefreshInputBody(refreshToken = refreshToken),
            )
            RefreshOutcome.Renewed(
                accessToken = response.accessToken,
                refreshToken = response.refreshToken,
                expiresInSeconds = response.expiresIn,
            )
        } catch (e: ClientException) {
            if (e.statusCode == HTTP_UNAUTHORIZED || e.statusCode == HTTP_FORBIDDEN) {
                RefreshOutcome.Rejected
            } else {
                RefreshOutcome.Unavailable
            }
        } catch (e: ServerException) {
            RefreshOutcome.Unavailable
        } catch (e: IOException) {
            RefreshOutcome.Unavailable
        } catch (e: Exception) {
            RefreshOutcome.Unavailable
        }
    }
}
