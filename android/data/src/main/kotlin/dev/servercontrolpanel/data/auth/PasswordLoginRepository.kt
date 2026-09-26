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

/**
 * Marker that separates "second factor refused" from "wrong username or
 * password" within the same 401: the BFF answers `"invalid 2FA code"` vs
 * `"invalid credentials"` (see `internal/mobilebff/auth_passkey.go`). Matching on
 * "2fa" alone survives wording changes; if it still fails, the caller falls back
 * to whether a code was sent.
 */
private const val SECOND_FACTOR_REJECTED = "2fa"

/** Result of `POST /auth/login`; see `internal/mobilebff/auth_login.go`. */
sealed interface PasswordLoginResult {

    data class Success(val accessToken: String, val refreshToken: String, val expiresInSeconds: Long) :
        PasswordLoginResult

    /**
     * The user has a second factor and no code was sent. Not an error: the screen
     * asks for the code and calls again (`{"totp_required": true}`).
     */
    data object TotpRequired : PasswordLoginResult

    /**
     * The second factor was refused after the username and password passed. The
     * screen stays on the code step and clears only the code; blaming the password
     * would push the user towards the server's 5-attempt lockout.
     */
    data class InvalidCode(val reason: String) : PasswordLoginResult

    data class Failed(val reason: String) : PasswordLoginResult
}

/**
 * The slice of [PasswordLoginRepository] the login screen depends on, so callers
 * outside `:data` can test against a fake.
 */
interface PasswordLoginSource {
    suspend fun login(username: String, password: String, totpCode: String? = null): PasswordLoginResult
}

/**
 * The single call site into the generated client for password login
 * (`AuthApi.mobileLogin`); callers only see [PasswordLoginResult].
 *
 * Password login is the secondary path (passkey is primary). It exists because a
 * passkey registered by QR only works once approved in the panel, so a fresh
 * device would otherwise have no way in.
 */
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

/**
 * Translates a 4xx from `POST /auth/login` into what the screen does next.
 *
 * A 401 means either a wrong password or a wrong second factor. The server's
 * `detail` decides first (see [SECOND_FACTOR_REJECTED]); if the body is missing,
 * [sentCode] decides, since the server only asks for a code after accepting the
 * password. `internal` so it is testable without Android or the generated client.
 */
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
            // A deliberately generic message: the error body comes from the
            // BFF's public surface and must not be echoed raw.
            else -> "Could not sign in (error $statusCode)."
        },
    )
}

/**
 * The label shown in the panel's list of mobile sessions (`device_label`):
 * manufacturer plus model, which the operator recognises when revoking.
 */
internal fun defaultDeviceLabel(): String = listOf(Build.MANUFACTURER, Build.MODEL)
    .filter { it.isNotBlank() }
    .joinToString(" ")
    .ifBlank { "Android" }
