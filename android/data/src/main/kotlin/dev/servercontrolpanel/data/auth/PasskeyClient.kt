package dev.servercontrolpanel.data.auth

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
import dev.servercontrolpanel.data.config.ServerConfigRepository
import dev.servercontrolpanel.mobileapiclient.api.AuthApi
import dev.servercontrolpanel.mobileapiclient.infrastructure.ApiClient
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientError
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.RequestConfig
import dev.servercontrolpanel.mobileapiclient.infrastructure.RequestMethod
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerError
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import dev.servercontrolpanel.mobileapiclient.infrastructure.Success
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

class RpIdMismatchException(val expected: String, val actual: String?) : Exception(
    "Challenge RP ID ($actual) does not match the configured host ($expected)",
)

private fun unwrapPublicKey(challengeJson: String): String {
    val root = Json.parseToJsonElement(challengeJson).jsonObject
    val publicKey = root["publicKey"]
        ?: throw IllegalArgumentException("The server challenge does not contain \"publicKey\".")
    return publicKey.toString()
}

fun buildCreateRequestJson(challengeJson: String): String = unwrapPublicKey(challengeJson)

fun buildGetRequestJson(challengeJson: String): String = unwrapPublicKey(challengeJson)

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

sealed interface RegistrationResult {
    data object PendingApproval : RegistrationResult
    data class Failed(val error: PasskeyError) : RegistrationResult
}

sealed interface LoginResult {
    data class Success(val accessToken: String, val refreshToken: String, val expiresIn: Long) : LoginResult

    data object PendingApproval : LoginResult
    data class Failed(val error: PasskeyError) : LoginResult
}

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

    private fun currentBaseUrl(): String = serverConfigRepository.currentBaseUrl()
        ?: error("Server not configured — set up the server before authenticating.")

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
