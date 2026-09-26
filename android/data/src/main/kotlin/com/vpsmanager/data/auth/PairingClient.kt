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
 * A pairing ticket is single-use and expires in minutes (see
 * `auth.IssuePairingTicket`, `internal/auth/tokens.go`): 24 random bytes,
 * hex-encoded, so exactly 48 lowercase hex characters. This regex is the
 * client-side half of the malformed-QR denial-of-service defence: a QR code
 * is attacker-controlled camera input, so [PairingClient.parsePairingPayload]
 * rejects anything that cannot possibly be a real ticket — arbitrary length
 * text, URLs, binary garbage — before a single byte of it reaches the
 * network.
 */
private val PAIRING_TICKET_SHAPE = Regex("^[0-9a-f]{48}$")

/**
 * Envelope version this build understands. The desktop panel
 * (`auth.PairingEnvelopeVersion`, `internal/auth/tokens.go`) is the source
 * of truth this must track; a mismatch means either an old app scanning a
 * new panel's QR or vice-versa, and this client fails closed (rejects the
 * payload) rather than guessing at a shape it has never seen.
 */
private const val SUPPORTED_PAIRING_ENVELOPE_VERSION = 1

/** Camera input this large cannot possibly be a real pairing envelope. */
private const val MAX_PAYLOAD_LENGTH = 4096

/**
 * The exact JSON envelope a pairing QR code encodes (`handleMobilePairStart`,
 * `internal/api/handlers_auth.go`) — ticket plus the server this device
 * should talk to, so the person pairing a phone never types a hostname by
 * hand. [serverUrl] alone never authorizes anything: it only becomes the
 * app's configured server through [com.vpsmanager.data.config.ServerConfigRepository.configure],
 * which refuses to silently repoint an already-paired device (see that
 * class's `RepointBlocked` doc).
 */
@Serializable
private data class PairingQrEnvelope(
    val v: Int,
    val ticket: String,
    @SerialName("server_url") val serverUrl: String,
)

private val pairingJson = Json { ignoreUnknownKeys = true }

/**
 * A decoded, shape-validated pairing QR payload — the only form a caller
 * outside [PairingClient] ever sees. [ticket] still matches
 * [PAIRING_TICKET_SHAPE]; [serverUrl] is passed through as-is and is NOT
 * yet validated as a real URL — that is
 * [com.vpsmanager.data.config.ServerConfigRepository.configure]'s job
 * (via `validateServerUrl`), so there is exactly one URL validator in the
 * app, not two.
 */
data class PairingPayload(val ticket: String, val serverUrl: String)

/**
 * Outcome of exchanging a scanned pairing ticket for a passkey-registration
 * authorization. [Authorized.regToken] only authorizes ONE subsequent call
 * to `POST /auth/passkey/register/begin` ([PasskeyClient.register]) — it is
 * never itself a credential, never a session, and scanning a QR code alone
 * never grants access to anything (`auth_pairing.go`).
 */
sealed interface PairingResult {
    data class Authorized(val regToken: String) : PairingResult
    data class Error(val reason: String) : PairingResult
}

/**
 * The single call site into the generated mobile BFF client
 * (`:data:mobile-api-client`) for `POST /api/mobile/v1/auth/pair`. No other
 * module may reference [AuthApi] or its generated model types directly for
 * pairing — callers only ever see [PairingResult]. Pairs with
 * [PasskeyClient], which owns everything from `regToken` onward.
 */
open class PairingClient(
    private val authApi: AuthApi = AuthApi(),
) {

    /**
     * Decodes [qrPayload] — the raw text read from the scanned QR code — as
     * the versioned envelope `handleMobilePairStart` mints
     * (`internal/api/handlers_auth.go`). Returns `null` for anything that is
     * not a well-formed envelope THIS BUILD understands: not JSON, an
     * unrecognized `v`, a `ticket` that does not match
     * [PAIRING_TICKET_SHAPE], or a blank `server_url`. Rejecting rather than
     * best-effort-parsing an envelope this client cannot fully validate is
     * deliberate (the payload is attacker-controlled camera input)
     * — the caller treats `null` exactly like "not a QR code we recognize"
     * and keeps scanning instead of forwarding partially-understood data
     * anywhere.
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
     * Consumes [ticket] via `POST /auth/pair`. Success only ever yields a
     * `reg_token` for one registration ceremony — never an access or refresh
     * token (see [PairingResult.Authorized] doc). An expired, already-used,
     * or never-issued ticket all fail identically server-side
     * (`ErrPairingTicketInvalid`): this client does not attempt to
     * distinguish them either, to avoid leaking which case occurred.
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
