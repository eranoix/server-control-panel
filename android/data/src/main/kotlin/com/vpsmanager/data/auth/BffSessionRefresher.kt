package com.vpsmanager.data.auth

import com.vpsmanager.data.config.ServerConfigRepository
import com.vpsmanager.mobileapiclient.api.AuthApi
import com.vpsmanager.mobileapiclient.infrastructure.ClientException
import com.vpsmanager.mobileapiclient.infrastructure.ServerException
import com.vpsmanager.mobileapiclient.model.MobileRefreshInputBody
import java.io.IOException

private const val HTTP_UNAUTHORIZED = 401
private const val HTTP_FORBIDDEN = 403

/**
 * The production implementation of [SessionRefresher]: the BFF's
 * `POST /auth/refresh` (`internal/mobilebff/auth_login.go`, operation
 * `mobileRefresh`).
 *
 * The endpoint does MANDATORY ROTATION — the refresh token that was sent stops
 * being valid on this call, whether it succeeds or not. Two consequences the
 * code above depends on:
 *  - the new pair MUST be stored (which is what [SessionManager] does in
 *    `adopt(persist = true)`); losing the response is losing the session;
 *  - a 401 here is final, never "try again" — hence [RefreshOutcome.Rejected]
 *    tearing the session down instead of rescheduling.
 *
 * The `basePath` is derived from [serverConfigRepository] on every call, and
 * not from the `System.getProperty` that `AuthApi.defaultBasePath` reads a
 * single time in a `by lazy` — same pattern as [PasskeyClient], so that
 * switching servers does not leave a stale basePath stuck in a static field.
 */
class BffSessionRefresher(
    private val serverConfigRepository: ServerConfigRepository,
    private val authApiFactory: (String) -> AuthApi = { basePath -> AuthApi(basePath) },
) : SessionRefresher {

    override suspend fun refresh(refreshToken: String): RefreshOutcome {
        val basePath = serverConfigRepository.currentBaseUrl()?.let { "$it/api/mobile/v1" }
            // With no server configured there is nowhere to renew. This is not
            // a transient error: it is "this install has not been paired yet".
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
            // An unexpectedly shaped response and the like. Never tears the
            // session down over it — the refresh token may still be valid.
            RefreshOutcome.Unavailable
        }
    }
}
