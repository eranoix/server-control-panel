package dev.servercontrolpanel.data.auth

import android.os.Build
import dev.servercontrolpanel.data.config.ServerConfigRepository
import dev.servercontrolpanel.mobileapiclient.api.AuthApi
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientError
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import dev.servercontrolpanel.mobileapiclient.model.MobileLoginInputBody
import java.io.IOException

private const val HTTP_UNAUTHORIZED = 401
private const val HTTP_LOCKED = 423
private const val HTTP_TOO_MANY_REQUESTS = 429

private const val SECOND_FACTOR_REJECTED = "2fa"

sealed interface PasswordLoginResult {

    data class Success(val accessToken: String, val refreshToken: String, val expiresInSeconds: Long) :
        PasswordLoginResult

    data object TotpRequired : PasswordLoginResult

    data class InvalidCode(val reason: String) : PasswordLoginResult

    data class Failed(val reason: String) : PasswordLoginResult
}

interface PasswordLoginSource {
    suspend fun login(username: String, password: String, totpCode: String? = null): PasswordLoginResult
}

class PasswordLoginRepository(
    private val serverConfigRepository: ServerConfigRepository,
    private val deviceLabel: String = defaultDeviceLabel(),
    private val authApiFactory: (String) -> AuthApi = { basePath -> AuthApi(basePath) },
) : PasswordLoginSource {

    override suspend fun login(username: String, password: String, totpCode: String?): PasswordLoginResult {
        val basePath = serverConfigRepository.currentBaseUrl()?.let { "$it/api/mobile/v1" }
            ?: return PasswordLoginResult.Failed("No server configured on this device.")
        return try {
            val response = authApiFactory(basePath).mobileLogin(
                MobileLoginInputBody(
                    password = password,
                    username = username,
                    deviceLabel = deviceLabel,
                    totpCode = totpCode?.takeIf { it.isNotBlank() },
                ),
            )
            val accessToken = response.accessToken
            val refreshToken = response.refreshToken
            val expiresIn = response.expiresIn
            when {
                response.totpRequired == true -> PasswordLoginResult.TotpRequired
                accessToken != null && refreshToken != null && expiresIn != null ->
                    PasswordLoginResult.Success(accessToken, refreshToken, expiresIn)
                else -> PasswordLoginResult.Failed("Unexpected response from the server.")
            }
        } catch (e: ClientException) {
            translateLoginFailure(
                statusCode = e.statusCode,
                errorBody = (e.response as? ClientError<*>)?.body as? String,
                sentCode = !totpCode.isNullOrBlank(),
            )
        } catch (e: ServerException) {
            PasswordLoginResult.Failed("The server is unavailable right now.")
        } catch (e: IOException) {
            PasswordLoginResult.Failed("Connection failed. Check your network and try again.")
        } catch (e: Exception) {
            PasswordLoginResult.Failed("Could not sign in.")
        }
    }
}

internal fun translateLoginFailure(statusCode: Int, errorBody: String?, sentCode: Boolean): PasswordLoginResult {
    val wasSecondFactor = when {
        errorBody.isNullOrBlank() -> sentCode
        else -> errorBody.contains(SECOND_FACTOR_REJECTED, ignoreCase = true)
    }
    if (statusCode == HTTP_UNAUTHORIZED && wasSecondFactor) {
        return PasswordLoginResult.InvalidCode(
            "Invalid or expired code. The authenticator app code changes every 30 seconds — " +
                "get a new one and type it in full. A backup code also works.",
        )
    }
    return PasswordLoginResult.Failed(
        when (statusCode) {
            HTTP_UNAUTHORIZED -> "Invalid username or password."
            HTTP_LOCKED ->
                "Account temporarily locked after repeated attempts — wrong verification codes " +
                    "count too. Wait a few minutes and try again."
            HTTP_TOO_MANY_REQUESTS -> "Too many attempts. Wait a moment and try again."
            else -> "Could not sign in (error $statusCode)."
        },
    )
}

internal fun defaultDeviceLabel(): String = listOf(Build.MANUFACTURER, Build.MODEL)
    .filter { it.isNotBlank() }
    .joinToString(" ")
    .ifBlank { "Android" }
