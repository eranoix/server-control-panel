package com.vpsmanager.data.auth

import com.vpsmanager.mobileapiclient.api.AuthApi
import com.vpsmanager.mobileapiclient.infrastructure.ClientException
import com.vpsmanager.mobileapiclient.infrastructure.ServerException
import com.vpsmanager.mobileapiclient.model.PairingConsumeInputBody
import java.io.IOException
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json

/**
 * A pairing ticket is single use and expires in minutes: 24 random bytes as 48
 * lowercase hex characters (`internal/auth/tokens.go`). A QR code is
 * attacker-controlled input, so anything else is rejected before touching the network.
 */
private val PAIRING_TICKET_SHAPE = Regex("^[0-9a-f]{48}$")

/**
 * Envelope version this build understands; must track `auth.PairingEnvelopeVersion`
 * (`internal/auth/tokens.go`). A mismatch fails closed.
 */
private const val SUPPORTED_PAIRING_ENVELOPE_VERSION = 1

/** Camera input this large cannot possibly be a real pairing envelope. */
private const val MAX_PAYLOAD_LENGTH = 4096

/**
 * The JSON envelope a pairing QR code encodes (`handleMobilePairStart`,
 * `internal/api/handlers_auth.go`): the ticket plus the server URL, so the user
 * never types a hostname. [serverUrl] authorizes nothing by itself;
 * [com.vpsmanager.data.config.ServerConfigRepository.configure] refuses to
 * silently repoint an already paired device.
 */
@Serializable
private data class PairingQrEnvelope(
    val v: Int,
    val ticket: String,
    @SerialName("server_url") val serverUrl: String,
)

private val pairingJson = Json { ignoreUnknownKeys = true }

/**
 * A decoded, shape-validated pairing payload. [serverUrl] is not validated here:
 * [com.vpsmanager.data.config.ServerConfigRepository.configure] does that, so the
 * app has a single URL validator.
 */
data class PairingPayload(val ticket: String, val serverUrl: String)

/**
 * Outcome of exchanging a pairing ticket. [Authorized.regToken] only authorizes
 * one `POST /auth/passkey/register/begin` ([PasskeyClient.register]); it is never
 * a credential or a session.
 */
sealed interface PairingResult {
    data class Authorized(val regToken: String) : PairingResult
    data class Error(val reason: String) : PairingResult
}

/**
 * The single call site into the generated client for `POST /api/mobile/v1/auth/pair`;
 * callers only see [PairingResult]. [PasskeyClient] owns everything from `regToken` on.
 */
open class PairingClient(
    private val authApi: AuthApi = AuthApi(),
) {

    /**
     * Decodes the raw QR text as the versioned envelope. Returns `null` for anything
     * this build cannot fully validate (not JSON, unknown `v`, malformed `ticket`,
     * blank `server_url`); the caller then keeps scanning.
     */
    fun parsePairingPayload(qrPayload: String): PairingPayload? {
        val candidate = qrPayload.trim()
        if (candidate.isEmpty() || candidate.length > MAX_PAYLOAD_LENGTH) return null
        val envelope = try {
            pairingJson.decodeFromString(PairingQrEnvelope.serializer(), candidate)
        } catch (e: SerializationException) {
            return null
        } catch (e: IllegalArgumentException) {
            return null
        }
        if (envelope.v != SUPPORTED_PAIRING_ENVELOPE_VERSION) return null
        if (!PAIRING_TICKET_SHAPE.matches(envelope.ticket)) return null
        val serverUrl = envelope.serverUrl.trim()
        if (serverUrl.isEmpty()) return null
        return PairingPayload(ticket = envelope.ticket, serverUrl = serverUrl)
    }

    /**
     * Consumes [ticket] via `POST /auth/pair`, yielding only a one-time `reg_token`.
     * Expired, used and unknown tickets fail identically on the server, and this
     * client does not try to tell them apart either.
     */
    suspend fun consume(ticket: String): PairingResult = try {
        val response = authApi.mobilePairConsume(PairingConsumeInputBody(ticket = ticket))
        PairingResult.Authorized(regToken = response.regToken)
    } catch (e: ClientException) {
        val reason = when (e.statusCode) {
            401, 403, 404, 410 ->
                "This code has expired or was already used. Go back to the panel and generate a new QR code."
            429 -> "Too many attempts. Wait a moment and try again."
            else -> "Could not confirm the pairing (error ${e.statusCode})."
        }
        PairingResult.Error(reason)
    } catch (e: ServerException) {
        PairingResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        PairingResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        PairingResult.Error("Configuration error while pairing the device.")
    } catch (e: UnsupportedOperationException) {
        PairingResult.Error("Unexpected response from the server.")
    }
}
