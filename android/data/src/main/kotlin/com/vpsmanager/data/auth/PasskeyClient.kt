package com.vpsmanager.data.auth

import android.content.Context
import androidx.credentials.CreatePublicKeyCredentialRequest
import androidx.credentials.CreatePublicKeyCredentialResponse
import androidx.credentials.CredentialManager
import androidx.credentials.GetCredentialRequest
import androidx.credentials.GetPublicKeyCredentialOption
import androidx.credentials.PublicKeyCredential
import androidx.credentials.exceptions.CreateCredentialCancellationException
import androidx.credentials.exceptions.CreateCredentialException
import androidx.credentials.exceptions.CreateCredentialInterruptedException
import androidx.credentials.exceptions.CreateCredentialNoCreateOptionException
import androidx.credentials.exceptions.CreateCredentialUnsupportedException
import androidx.credentials.exceptions.GetCredentialCancellationException
import androidx.credentials.exceptions.GetCredentialException
import androidx.credentials.exceptions.GetCredentialInterruptedException
import androidx.credentials.exceptions.GetCredentialUnsupportedException
import androidx.credentials.exceptions.NoCredentialException
import androidx.credentials.exceptions.domerrors.NetworkError
import androidx.credentials.exceptions.domerrors.NotAllowedError
import androidx.credentials.exceptions.domerrors.NotSupportedError
import androidx.credentials.exceptions.domerrors.SecurityError
import androidx.credentials.exceptions.publickeycredential.CreatePublicKeyCredentialDomException
import androidx.credentials.exceptions.publickeycredential.GetPublicKeyCredentialDomException
import com.vpsmanager.data.config.ServerConfigRepository
import com.vpsmanager.mobileapiclient.api.AuthApi
import com.vpsmanager.mobileapiclient.infrastructure.ApiClient
import com.vpsmanager.mobileapiclient.infrastructure.ClientError
import com.vpsmanager.mobileapiclient.infrastructure.ClientException
import com.vpsmanager.mobileapiclient.infrastructure.RequestConfig
import com.vpsmanager.mobileapiclient.infrastructure.RequestMethod
import com.vpsmanager.mobileapiclient.infrastructure.ServerError
import com.vpsmanager.mobileapiclient.infrastructure.ServerException
import com.vpsmanager.mobileapiclient.infrastructure.Success
import java.io.IOException
import java.net.URI
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import okhttp3.Call

/**
 * Thrown by [verifyRpId] when the server's relying-party id does not match
 * the host this app is actually configured to talk to. Never caught and
 * silently ignored anywhere in this file — see [PasskeyError.RpIdMismatch],
 * which is the only place this turns into user-facing behavior.
 */
class RpIdMismatchException(val expected: String, val actual: String?) : Exception(
    "Challenge RP ID ($actual) does not match the configured host ($expected)",
)

/**
 * The go-webauthn server (`internal/api/passkey.go`, `BeginPasskeyRegistration`/
 * `BeginPasskeyLogin`) marshals `*protocol.CredentialCreation`/
 * `*protocol.CredentialAssertion` directly, which — mirroring the browser
 * call shape `navigator.credentials.create({publicKey: options})` —
 * wraps the actual options under a top-level `"publicKey"` key:
 * `{"publicKey": {...}, "mediation": "..."}`. Android's
 * `CreatePublicKeyCredentialRequest`/`GetPublicKeyCredentialOption` expect
 * the UNWRAPPED inner object instead (Google's passkey codelab JSON shape).
 * Every function in this file that touches a raw challenge JSON goes
 * through this single unwrap point.
 */
private fun unwrapPublicKey(challengeJson: String): String {
    val root = Json.parseToJsonElement(challengeJson).jsonObject
    val publicKey = root["publicKey"]
        ?: throw IllegalArgumentException("The server challenge does not contain \"publicKey\".")
    return publicKey.toString()
}

/**
 * Builds the `requestJson` for `CreatePublicKeyCredentialRequest` out of the
 * raw `options` the server returned from `/auth/passkey/register/begin`.
 * Named after the plan's `buildCreateRequest`; unlike a byte-identical copy
 * of the server's JSON, this UNWRAPS the `"publicKey"` envelope first — see
 * [unwrapPublicKey] for why the envelope cannot be forwarded as-is.
 */
fun buildCreateRequestJson(challengeJson: String): String = unwrapPublicKey(challengeJson)

/**
 * Builds the `requestJson` for `GetPublicKeyCredentialOption` out of the raw
 * `options` the server returned from `/auth/passkey/login/begin`. Same
 * unwrap rule as [buildCreateRequestJson] — see [unwrapPublicKey].
 */
fun buildGetRequestJson(challengeJson: String): String = unwrapPublicKey(challengeJson)

/**
 * Confirms the relying-party id embedded in a WebAuthn challenge matches
 * [configuredBaseUrl]'s host — the same `Config.PublicHostname` value the
 * server derives its RP ID from and `/.well-known/assetlinks.json` serves
 * for. Handles BOTH challenge shapes this server ever sends: registration
 * nests it at `publicKey.rp.id`; login puts it directly at
 * `publicKey.rpId` (`protocol.PublicKeyCredentialCreationOptions` vs
 * `protocol.PublicKeyCredentialRequestOptions`, go-webauthn v0.18.0). A
 * mismatched or absent RP ID always fails closed — this app must never
 * complete a WebAuthn ceremony against a host it wasn't configured for
 *
 */
fun verifyRpId(challengeJson: String, configuredBaseUrl: String): Result<Unit> {
    val publicKeyJson = try {
        unwrapPublicKey(challengeJson)
    } catch (e: Exception) {
        return Result.failure(e)
    }
    val publicKey = Json.parseToJsonElement(publicKeyJson).jsonObject
    val rpId = publicKey["rp"]?.jsonObject?.get("id")?.jsonPrimitive?.contentOrNull
        ?: publicKey["rpId"]?.jsonPrimitive?.contentOrNull
    val expectedHost = try {
        URI(configuredBaseUrl).host
    } catch (e: Exception) {
        null
    }
    return if (rpId != null && expectedHost != null && rpId == expectedHost) {
        Result.success(Unit)
    } else {
        Result.failure(RpIdMismatchException(expected = expectedHost.orEmpty(), actual = rpId))
    }
}

/**
 * Every distinct, actionable failure mode a passkey ceremony can hit —
 * deliberately never a single generic "erro" bucket. [message] is
 * pt-BR and safe to show verbatim; it never echoes a raw exception message
 * back to the screen.
 */
sealed interface PasskeyError {
    val message: String

    data object Cancelled : PasskeyError {
        override val message = "Operation canceled."
    }

    data object NoPasskeyAvailable : PasskeyError {
        override val message = "No approved passkey was found on this device for this site."
    }

    data object DeviceNotConfigured : PasskeyError {
        override val message =
            "This device has no screen lock or biometrics set up — set one up in the " +
                "system settings to use passkeys."
    }

    data class RpIdMismatch(val expected: String, val actual: String?) : PasskeyError {
        override val message = "The server does not match the domain configured in this app."
    }

    data object ServerAssociationInvalid : PasskeyError {
        override val message =
            "This app's association with the server (assetlinks.json) is not valid yet — try " +
                "again in a few minutes."
    }

    data object NetworkError : PasskeyError {
        override val message = "Connection failed. Check your network and try again."
    }

    data object ServerUnavailable : PasskeyError {
        override val message = "The server is unavailable right now."
    }

    data class Unknown(override val message: String) : PasskeyError
}

/** Outcome of [PasskeyClient.register]. */
sealed interface RegistrationResult {
    /**
     * The ONLY success shape `register/finish` ever returns
     * (`passkeyRegisterFinishOutput`) — the credential was
     * created but is inert until approved from an authenticated desktop
     * session. Never treat this as a login.
     */
    data object PendingApproval : RegistrationResult
    data class Failed(val error: PasskeyError) : RegistrationResult
}

/** Outcome of [PasskeyClient.login]. */
sealed interface LoginResult {
    data class Success(val accessToken: String, val refreshToken: String, val expiresIn: Long) : LoginResult

    /**
     * The credential matched but is not approved yet — the exact same
     * `{"error":"pending_approval"}` body `passkeyLoginFinishOutput` sends
     * (`auth_passkey.go`). Render distinctly from [Failed]: this is not a
     * wrong credential, it is a correct one still waiting on desktop
     * approval.
     */
    data object PendingApproval : LoginResult
    data class Failed(val error: PasskeyError) : LoginResult
}

/**
 * Drives the two passkey WebAuthn ceremonies (registration, discoverable
 * login) end to end: begin -> Credential Manager -> finish. Extends
 * [AuthApi] purely to reuse [ApiClient.request]'s HTTP/serialization
 * machinery — every generated typed model for these four operations
 * (`Passkey*BeginOutputBody.options`, `Passkey*FinishInputBody.credential`)
 * declares its payload as `@Contextual kotlin.Any?` with no contextual
 * serializer registered anywhere (`infrastructure/Serializer.kt`), so
 * calling the generated typed methods for these specific operations would
 * throw `SerializationException` the moment those fields are non-null.
 * [SduiDataClient] establishes the same `request<I, JsonElement>` bypass
 * for the same reason.
 */
open class PasskeyClient(
    private val serverConfigRepository: ServerConfigRepository,
    basePath: String = serverConfigRepository.currentBaseUrl()?.let { "$it/api/mobile/v1" } ?: AuthApi.defaultBasePath,
    client: Call.Factory = ApiClient.defaultClient,
) : AuthApi(basePath, client) {

    @Suppress("UNCHECKED_CAST")
    private suspend fun rawPost(path: String, body: JsonElement?): JsonElement? {
        val config = RequestConfig<JsonElement?>(
            method = RequestMethod.POST,
            path = path,
            query = mutableMapOf(),
            requiresAuthentication = false,
            body = body,
        )
        return when (val response = request<JsonElement?, JsonElement>(config)) {
            is Success<*> -> response.data as JsonElement?
            is ClientError<*> -> throw ClientException(
                "Client error : ${response.statusCode} ${response.message.orEmpty()}",
                response.statusCode,
                response,
            )
            is ServerError<*> -> throw ServerException(
                "Server error : ${response.statusCode} ${response.message.orEmpty()} ${response.body}",
                response.statusCode,
                response,
            )
            else -> throw UnsupportedOperationException("Unexpected response at $path")
        }
    }

    private fun httpFailure(e: Exception): PasskeyError = when (e) {
        is ClientException -> when (e.statusCode) {
            401, 403, 404, 410 -> PasskeyError.Unknown(
                "This ceremony has expired or was already used. Restart the pairing/sign-in.",
            )
            429 -> PasskeyError.Unknown("Too many attempts. Wait a moment and try again.")
            else -> PasskeyError.Unknown("Could not complete the operation (error ${e.statusCode}).")
        }
        is ServerException -> PasskeyError.ServerUnavailable
        is IOException -> PasskeyError.NetworkError
        else -> PasskeyError.Unknown("Unexpected response from the server.")
    }

    /**
     * The server this device is configured against, taken from
     * [serverConfigRepository] — never `System.getProperty` directly, and
     * never a `localhost` guess. A ceremony started with no configured
     * server is a bug in the caller (the app gates every screen behind
     * server setup — see `MainActivity`), so this fails loud instead of
     * silently trusting whatever [baseUrl] happened to resolve to.
     */
    private fun currentBaseUrl(): String = serverConfigRepository.currentBaseUrl()
        ?: error("Server not configured — set up the server before authenticating.")

    /**
     * Completes the QR-pairing registration ceremony for [regToken]
     * (obtained from [PairingClient.consume]): begin -> RP ID check ->
     * Credential Manager `createCredential` -> finish. Never returns
     * anything resembling a session — see [RegistrationResult.PendingApproval].
     */
    suspend fun register(context: Context, regToken: String, label: String? = null): RegistrationResult {
        val beginBody = try {
            rawPost(
                "auth/passkey/register/begin",
                buildJsonObject { put("reg_token", regToken) },
            )
        } catch (e: ClientException) {
            return RegistrationResult.Failed(httpFailure(e))
        } catch (e: ServerException) {
            return RegistrationResult.Failed(httpFailure(e))
        } catch (e: IOException) {
            return RegistrationResult.Failed(httpFailure(e))
        }
        val begin = beginBody?.jsonObject
            ?: return RegistrationResult.Failed(PasskeyError.Unknown("Empty response from the server."))
        val continuationToken = begin["continuation_token"]?.jsonPrimitive?.contentOrNull
            ?: return RegistrationResult.Failed(PasskeyError.Unknown("Server response without continuation_token."))
        val options = begin["options"]
            ?: return RegistrationResult.Failed(PasskeyError.Unknown("Server response without options."))
        val challengeJson = options.toString()

        verifyRpId(challengeJson, currentBaseUrl()).onFailure { e ->
            return RegistrationResult.Failed(
                if (e is RpIdMismatchException) {
                    PasskeyError.RpIdMismatch(e.expected, e.actual)
                } else {
                    PasskeyError.Unknown("Invalid server challenge.")
                },
            )
        }

        val requestJson = try {
            buildCreateRequestJson(challengeJson)
        } catch (e: IllegalArgumentException) {
            return RegistrationResult.Failed(PasskeyError.Unknown("Invalid server challenge."))
        }

        val credentialManager = CredentialManager.create(context)
        val response = try {
            credentialManager.createCredential(
                context = context,
                request = CreatePublicKeyCredentialRequest(requestJson = requestJson),
            )
        } catch (e: CreateCredentialCancellationException) {
            return RegistrationResult.Failed(PasskeyError.Cancelled)
        } catch (e: CreateCredentialNoCreateOptionException) {
            return RegistrationResult.Failed(PasskeyError.NoPasskeyAvailable)
        } catch (e: CreateCredentialUnsupportedException) {
            return RegistrationResult.Failed(PasskeyError.DeviceNotConfigured)
        } catch (e: CreateCredentialInterruptedException) {
            return RegistrationResult.Failed(PasskeyError.Unknown("Operation interrupted, try again."))
        } catch (e: CreatePublicKeyCredentialDomException) {
            return RegistrationResult.Failed(mapCreateDomError(e))
        } catch (e: CreateCredentialException) {
            return RegistrationResult.Failed(PasskeyError.Unknown(e.message ?: "Failed to create the passkey."))
        }

        val registrationJson = (response as CreatePublicKeyCredentialResponse).registrationResponseJson
        val finishBody = try {
            rawPost(
                "auth/passkey/register/finish",
                buildJsonObject {
                    put("continuation_token", continuationToken)
                    put("credential", Json.parseToJsonElement(registrationJson))
                    if (!label.isNullOrBlank()) put("label", label)
                },
            )
        } catch (e: ClientException) {
            return RegistrationResult.Failed(httpFailure(e))
        } catch (e: ServerException) {
            return RegistrationResult.Failed(httpFailure(e))
        } catch (e: IOException) {
            return RegistrationResult.Failed(httpFailure(e))
        }
        val status = finishBody?.jsonObject?.get("status")?.jsonPrimitive?.contentOrNull
        return if (status == "pending_approval") {
            RegistrationResult.PendingApproval
        } else {
            RegistrationResult.Failed(PasskeyError.Unknown("Unexpected response from the server."))
        }
    }

    /**
     * Completes a discoverable passkey login: begin -> RP ID check ->
     * Credential Manager `getCredential` -> finish. A credential that
     * exists but is not yet approved fails with [LoginResult.PendingApproval],
     * never with [LoginResult.Failed] — see `passkeyLoginFinishOutput.error`
     * ("pending_approval") in `auth_passkey.go`.
     */
    suspend fun login(context: Context): LoginResult {
        val beginBody = try {
            rawPost("auth/passkey/login/begin", body = null)
        } catch (e: ClientException) {
            return LoginResult.Failed(httpFailure(e))
        } catch (e: ServerException) {
            return LoginResult.Failed(httpFailure(e))
        } catch (e: IOException) {
            return LoginResult.Failed(httpFailure(e))
        }
        val begin = beginBody?.jsonObject
            ?: return LoginResult.Failed(PasskeyError.Unknown("Empty response from the server."))
        val continuationToken = begin["continuation_token"]?.jsonPrimitive?.contentOrNull
            ?: return LoginResult.Failed(PasskeyError.Unknown("Server response without continuation_token."))
        val options = begin["options"]
            ?: return LoginResult.Failed(PasskeyError.Unknown("Server response without options."))
        val challengeJson = options.toString()

        verifyRpId(challengeJson, currentBaseUrl()).onFailure { e ->
            return LoginResult.Failed(
                if (e is RpIdMismatchException) {
                    PasskeyError.RpIdMismatch(e.expected, e.actual)
                } else {
                    PasskeyError.Unknown("Invalid server challenge.")
                },
            )
        }

        val requestJson = try {
            buildGetRequestJson(challengeJson)
        } catch (e: IllegalArgumentException) {
            return LoginResult.Failed(PasskeyError.Unknown("Invalid server challenge."))
        }

        val credentialManager = CredentialManager.create(context)
        val response = try {
            credentialManager.getCredential(
                context = context,
                request = GetCredentialRequest(
                    credentialOptions = listOf(GetPublicKeyCredentialOption(requestJson = requestJson)),
                ),
            )
        } catch (e: GetCredentialCancellationException) {
            return LoginResult.Failed(PasskeyError.Cancelled)
        } catch (e: NoCredentialException) {
            return LoginResult.Failed(PasskeyError.NoPasskeyAvailable)
        } catch (e: GetCredentialUnsupportedException) {
            return LoginResult.Failed(PasskeyError.DeviceNotConfigured)
        } catch (e: GetCredentialInterruptedException) {
            return LoginResult.Failed(PasskeyError.Unknown("Operation interrupted, try again."))
        } catch (e: GetPublicKeyCredentialDomException) {
            return LoginResult.Failed(mapGetDomError(e))
        } catch (e: GetCredentialException) {
            return LoginResult.Failed(PasskeyError.Unknown(e.message ?: "Failed to authenticate with the passkey."))
        }

        val credential = response.credential as? PublicKeyCredential
            ?: return LoginResult.Failed(PasskeyError.Unknown("Credential in an unexpected format."))
        val finishBody = try {
            rawPost(
                "auth/passkey/login/finish",
                buildJsonObject {
                    put("continuation_token", continuationToken)
                    put("credential", Json.parseToJsonElement(credential.authenticationResponseJson))
                },
            )
        } catch (e: ClientException) {
            return LoginResult.Failed(httpFailure(e))
        } catch (e: ServerException) {
            return LoginResult.Failed(httpFailure(e))
        } catch (e: IOException) {
            return LoginResult.Failed(httpFailure(e))
        }
        val finish = finishBody?.jsonObject
            ?: return LoginResult.Failed(PasskeyError.Unknown("Empty response from the server."))
        val accessToken = finish["access_token"]?.jsonPrimitive?.contentOrNull
        val refreshToken = finish["refresh_token"]?.jsonPrimitive?.contentOrNull
        val expiresIn = finish["expires_in"]?.jsonPrimitive?.contentOrNull?.toLongOrNull()
        val error = finish["error"]?.jsonPrimitive?.contentOrNull
        return when {
            error == "pending_approval" -> LoginResult.PendingApproval
            accessToken != null && refreshToken != null && expiresIn != null ->
                LoginResult.Success(accessToken, refreshToken, expiresIn)
            else -> LoginResult.Failed(PasskeyError.Unknown("Unexpected response from the server."))
        }
    }

    private fun mapCreateDomError(e: CreatePublicKeyCredentialDomException): PasskeyError = when (e.domError) {
        is SecurityError -> PasskeyError.ServerAssociationInvalid
        is NotAllowedError -> PasskeyError.Cancelled
        is NotSupportedError -> PasskeyError.DeviceNotConfigured
        is NetworkError -> PasskeyError.NetworkError
        else -> PasskeyError.Unknown(e.message ?: "Failed to create the passkey.")
    }

    private fun mapGetDomError(e: GetPublicKeyCredentialDomException): PasskeyError = when (e.domError) {
        is SecurityError -> PasskeyError.ServerAssociationInvalid
        is NotAllowedError -> PasskeyError.Cancelled
        is NotSupportedError -> PasskeyError.DeviceNotConfigured
        is NetworkError -> PasskeyError.NetworkError
        else -> PasskeyError.Unknown(e.message ?: "Failed to authenticate with the passkey.")
    }
}
