package dev.servercontrolpanel.data.sdui

import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientError
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.RequestMethod
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import java.io.IOException

/**
 * Only characters an action id is ever built from server-side (see the
 * registry keys in `internal/mobilebff/sdui`) -- refused before any network
 * call so a malformed or tampered id can never reach `/actions/{id}` as a
 * path-traversal or template-injection attempt.
 */
private val ACTION_ID_PATTERN = Regex("^[A-Za-z0-9._-]+$")

/**
 * Outcome of `POST /api/mobile/v1/actions/{action_id}` -- the single
 * mutation path a screen has. A plain domain shape, same spirit as
 * [SduiDataResult]: no caller ever sees a raw [ClientException] or has to
 * know which generated infrastructure type carries a 422 body.
 */
sealed interface SduiActionHttpResult {
    /**
     * 200 -- `body` is the raw `{"patch": ...}` or `{"invalidate": [...]}`
     * object; interpreting which one it is belongs to the caller, not this
     * repository.
     */
    data class Success(val body: JsonObject) : SduiActionHttpResult

    /**
     * 422 -- `rawBody` is the untouched response text
     * (`{"error":"validation_failed","fields":{...}}`), decoded by the
     * caller against `dev.servercontrolpanel.core.sdui.SduiValidationError` with
     * `dev.servercontrolpanel.core.sdui.SduiJson`.
     */
    data class ValidationFailed(val rawBody: String) : SduiActionHttpResult

    /**
     * 404 -- unknown action id and "not permitted for this viewer" are the
     * same response on the wire (`{"error":"unknown_action"}`) by design;
     * this repository preserves that, it never tries to tell them apart.
     */
    data object NotFound : SduiActionHttpResult

    /**
     * 403 -- the viewer's permissions or the underlying resource changed
     * since the screen was fetched; the caller's answer is always "refetch
     * the screen", never a retry of the same action.
     */
    data object Stale : SduiActionHttpResult

    data class Error(val reason: String) : SduiActionHttpResult
}

/**
 * The one call site into [SduiDataClient] for running an SDUI action. Mirrors
 * [SduiDataRepository]'s endpoint-boundary posture, but the endpoint here is
 * always this repository's own construction (`/api/mobile/v1/actions/{id}`),
 * never a server-supplied string -- an action descriptor is never delivered
 * to the client (confirmed against `sdui.ActionsFor` on the Go side, which is
 * unused outside its own tests), so [invoke]'s `actionId` is the only
 * untrusted input, and it is validated against [ACTION_ID_PATTERN] before it
 * is templated into a path.
 */
class SduiActionRepository(
    private val client: SduiDataClient = SduiDataClient(),
) {
    suspend fun invoke(actionId: String, requestBody: JsonObject): SduiActionHttpResult {
        if (!ACTION_ID_PATTERN.matches(actionId)) {
            return SduiActionHttpResult.Error("Invalid action id: $actionId")
        }
        return try {
            val body: JsonElement? = client.call(
                "/api/mobile/v1/actions/$actionId",
                RequestMethod.POST,
                requestBody,
            )
            SduiActionHttpResult.Success(body as? JsonObject ?: JsonObject(emptyMap()))
        } catch (e: ClientException) {
            when (e.statusCode) {
                404 -> SduiActionHttpResult.NotFound
                403 -> SduiActionHttpResult.Stale
                422 -> SduiActionHttpResult.ValidationFailed(
                    (e.response as? ClientError<*>)?.body as? String ?: "",
                )
                else -> SduiActionHttpResult.Error("Could not run the action (error ${e.statusCode}).")
            }
        } catch (e: ServerException) {
            SduiActionHttpResult.Error("The server is unavailable right now.")
        } catch (e: IOException) {
            SduiActionHttpResult.Error("Connection failed. Check your network and try again.")
        } catch (e: IllegalStateException) {
            SduiActionHttpResult.Error("Configuration error while running the action.")
        } catch (e: UnsupportedOperationException) {
            SduiActionHttpResult.Error("Unexpected response from the server.")
        }
    }
}
