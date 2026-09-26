package com.vpsmanager.data.auth

import android.os.Build
import com.vpsmanager.data.config.ServerConfigRepository
import com.vpsmanager.mobileapiclient.api.AuthApi
import com.vpsmanager.mobileapiclient.infrastructure.ClientError
import com.vpsmanager.mobileapiclient.infrastructure.ClientException
import com.vpsmanager.mobileapiclient.infrastructure.ServerException
import com.vpsmanager.mobileapiclient.model.MobileLoginInputBody
import java.io.IOException

private const val HTTP_UNAUTHORIZED = 401
private const val HTTP_LOCKED = 423
private const val HTTP_TOO_MANY_REQUESTS = 429

/**
 * The marker that separates "the second factor was refused" from "wrong
 * username or password" inside the SAME 401.
 *
 * The BFF already distinguishes the two cases in the error body —
 * `ErrMobileLoginInvalidCode` answers `detail: "invalid 2FA code"`, while a
 * wrong credential answers `"invalid credentials"` (see
 * `internal/mobilebff/auth_passkey.go`). The app threw that information away
 * and said "invalid username, password or code" in both cases: at the code step
 * that is a FALSE accusation against the password the server has just accepted
 * — and it sends the operator back to retype the password until they trip the
 * attempt lockout.
 *
 * It matches on "2fa" rather than on the whole sentence on purpose: that is the
 * piece that survives a change of accent or wording on the server side. If it
 * still does not match, the caller falls back to the CONTEXT criterion (did we
 * send a code?), so a server change degrades to the old behaviour instead of
 * breaking.
 */
private const val SEGUNDO_FATOR_RECUSADO = "2fa"

/** Resultado de `POST /auth/login` — ver `internal/mobilebff/auth_login.go`. */
sealed interface PasswordLoginResult {

    data class Success(val accessToken: String, val refreshToken: String, val expiresInSeconds: Long) :
        PasswordLoginResult

    /**
     * The user has a second factor enrolled and no code was sent. This is NOT
     * an error: it is the same `{"totp_required": true}` branch the desktop
     * panel's login already has. The screen should ask for the code and call
     * again.
     */
    data object TotpRequired : PasswordLoginResult

    /**
     * The SECOND FACTOR was refused — username and password already passed.
     *
     * Separate from [Failed] because the screen reacts differently: it keeps
     * the form on the code step (rather than throwing the operator back to the
     * password), clears only the code field, and says it was the code. A TOTP
     * code is worth ~30 seconds, so getting it wrong by being late is routine,
     * not an exception — and every error counts towards the server's attempt
     * lockout (5 failures), which makes the wrong message genuinely expensive.
     */
    data class InvalidCode(val reason: String) : PasswordLoginResult

    data class Failed(val reason: String) : PasswordLoginResult
}

/**
 * The slice of [PasswordLoginRepository] the login screen depends on — the same
 * convention as [com.vpsmanager.data.session.SessionSource]: anyone outside
 * `:data` tests against a fake without touching the generated client.
 */
interface PasswordLoginSource {
    suspend fun login(username: String, password: String, totpCode: String? = null): PasswordLoginResult
}

/**
 * The single call site into the generated client for password login
 * (`AuthApi.mobileLogin`). It follows the same boundary as
 * [com.vpsmanager.data.session.SessionRepository]: no module outside `:data`
 * references `AuthApi` or the generated DTOs — callers only ever see
 * [PasswordLoginResult].
 *
 * Password login is this app's SECONDARY path (the primary is passkey). It
 * exists because the primary has a prerequisite the operator cannot always
 * satisfy on the spot: a passkey registered by QR is born inert and only starts
 * logging in once it has been approved in the panel. Without password login, a
 * freshly installed device would have NO way in at all until somebody opened
 * the desktop.
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
            traduzFalhaDeLogin(
                statusCode = e.statusCode,
                corpoDoErro = (e.response as? ClientError<*>)?.body as? String,
                enviouCodigo = !totpCode.isNullOrBlank(),
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
 * Translates a 4xx response from `POST /auth/login` into what the SCREEN has to
 * do next.
 *
 * The 401 is the case that matters, because it covers TWO different accidents
 * calling for opposite reactions: a wrong password (go back to the password)
 * and a wrong second factor (stay on the code). Separating them:
 *
 * - Primary criterion: the server's own `detail`, which already distinguishes
 *   the two (see [SEGUNDO_FATOR_RECUSADO]). It is the exact criterion — it even
 *   catches the case where the operator edits the password at the code step and
 *   starts getting the PASSWORD wrong: the server then answers "invalid
 *   credentials" and the screen correctly goes back to talking about the
 *   password.
 * - Fallback criterion: [enviouCodigo]. If the body does not arrive (a proxy
 *   that swallows it, a generator that changes shape), it still holds that the
 *   server only asks for a code AFTER accepting the password — so a 401 on a
 *   call that carried a code is, overwhelmingly often, the code.
 *
 * A separate function, and `internal`, so it is testable without Android and
 * without the generated client: this is where the decision lives, not in the
 * `catch`.
 */
internal fun traduzFalhaDeLogin(statusCode: Int, corpoDoErro: String?, enviouCodigo: Boolean): PasswordLoginResult {
    val foiOSegundoFator = when {
        corpoDoErro.isNullOrBlank() -> enviouCodigo
        else -> corpoDoErro.contains(SEGUNDO_FATOR_RECUSADO, ignoreCase = true)
    }
    if (statusCode == HTTP_UNAUTHORIZED && foiOSegundoFator) {
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
 * The label that appears in the panel's list of mobile sessions
 * (`device_label` in `mobileLoginInput`). Manufacturer plus model is what the
 * operator recognises when scanning the list; an opaque id would help nobody
 * decide which session to revoke.
 */
internal fun defaultDeviceLabel(): String = listOf(Build.MANUFACTURER, Build.MODEL)
    .filter { it.isNotBlank() }
    .joinToString(" ")
    .ifBlank { "Android" }
