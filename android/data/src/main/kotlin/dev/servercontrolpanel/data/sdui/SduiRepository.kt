package dev.servercontrolpanel.data.sdui

import dev.servercontrolpanel.core.sdui.SduiEnvelope
import dev.servercontrolpanel.core.sdui.SduiJson
import dev.servercontrolpanel.core.sdui.parseScreen
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.RequestMethod
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import java.io.IOException

/**
 * Only characters a section id is ever built from server-side screen
 * descriptors -- refused before any network call so a malformed or
 * tampered id can never reach `/screens/{id}` as a path-traversal or
 * template-injection attempt. Mirrors `SduiActionRepository`'s
 * `ACTION_ID_PATTERN`.
 */
private val SECTION_ID_PATTERN = Regex("^[A-Za-z0-9._-]+$")

/**
 * Outcome of `GET /api/mobile/v1/screens/{section_id}`. A plain domain
 * shape, same spirit as [SduiDataResult]/[SduiActionHttpResult]: the caller
 * never sees a raw [ClientException] or has to guess which HTTP status a
 * given failure came from.
 */
sealed interface SduiScreenResult {
    data class Success(val envelope: SduiEnvelope) : SduiScreenResult

    /** 404 -- unknown section id for this viewer. */
    data object NotFound : SduiScreenResult

    /**
     * 403 -- the viewer is not permitted to see this section. Kept distinct
     * from [NotFound] here (unlike actions, where 403/404 are deliberately
     * conflated) because a screen host can react differently: an action's
     * 403 always means "refetch the screen", but a screen's own 403 means
     * "this route is not for you", which the host may want to show as an
     * explicit access-denied state rather than a generic error.
     */
    data object Forbidden : SduiScreenResult

    data class Error(val reason: String) : SduiScreenResult
}

/**
 * A narrow seam over [SduiRepository]: the only two members an SDUI screen host
 * (today `:feature-admin`) calls from the data layer.
 *
 * WHY THIS SEAM EXISTS. Without it, [SduiRepository] is a final class that only
 * knows how to talk to the real BFF — a feature-module test had nowhere to fake
 * at the DATA boundary and was pushed to the HTTP boundary (a `MockWebServer`
 * inside `feature/`), which is precisely what the gate
 * (`scripts/check-mobile-bff-only.sh`) forbids outside `:data`. The interface
 * is not a concession to testing: it declares what a host's surface actually
 * is — the same one [SduiRepository]'s KDoc already described in prose.
 *
 * It follows the narrow-seam pattern this repository already uses in
 * [dev.servercontrolpanel.data.videocall.LocalMediaTrackControl] and in
 * `TelecomAccountPort` (`feature/videocall/.../PhoneAccountRegistrar.kt`):
 * declare only what the consumer calls, keep the real implementation beside it,
 * and let the test inject a hand-written fake (there is no mocking library
 * here).
 *
 * It is not a `fun interface` because the host needs both members — reading the
 * descriptor ([screen]) and the single route to mutation ([action]). The other
 * half of what the host consumes, `SduiDataRepository::fetch`, does NOT get a
 * twin seam here: it already has one,
 * `dev.servercontrolpanel.sdui.actionrunner.ComponentDataFetcher`, and two interfaces
 * for the same boundary would be two names for one thing.
 */
interface SduiScreenPort {
    suspend fun screen(sectionId: String): SduiScreenResult

    suspend fun action(actionId: String, requestBody: JsonObject): SduiActionHttpResult
}

/**
 * The one place SDUI screen descriptors and actions cross the network,
 * through the generated BFF client. `:feature-admin` (and any future SDUI
 * host module) never touches [SduiDataClient] or the generated `MobileApi`
 * directly -- it only ever sees [SduiScreenResult] and [SduiActionHttpResult].
 *
 * [screen] deliberately does not call the generated `MobileApi.getScreen()`:
 * that method decodes its body into `kotlin.Any`, and kotlinx.serialization's
 * reified `decodeFromString<T>` has no [kotlinx.serialization.KSerializer]
 * for `Any` -- it would throw a [SerializationException] at runtime for
 * every single screen fetch. This repository instead reuses the same raw
 * [JsonElement] [SduiDataClient.call] path [SduiDataRepository] and
 * [SduiActionRepository] already use (which decodes into [JsonElement], a
 * type kotlinx.serialization *does* have a serializer for), re-encodes that
 * element to a JSON string, and hands it to `core.sdui.parseScreen` -- the
 * same parser [dev.servercontrolpanel.sdui.debug.PayloadPreviewScreen] exercises
 * against pasted fixtures.
 *
 * [action] adds no logic of its own; it delegates to [SduiActionRepository],
 * already covered by its own test suite, so this facade does not duplicate
 * (and risk drifting from) that outcome mapping.
 *
 * The abstract form of this facade is [SduiScreenPort] — it is what a host
 * declares its needs against, and what a feature test fakes.
 */
class SduiRepository(
    private val client: SduiDataClient = SduiDataClient(),
    private val actionRepository: SduiActionRepository = SduiActionRepository(client),
) : SduiScreenPort {
    override suspend fun screen(sectionId: String): SduiScreenResult {
        if (!SECTION_ID_PATTERN.matches(sectionId)) {
            return SduiScreenResult.Error("Invalid section id: $sectionId")
        }
        return try {
            val body: JsonElement = client.call(
                "/api/mobile/v1/screens/$sectionId",
                RequestMethod.GET,
            ) ?: return SduiScreenResult.Error("Empty response from the server.")
            val json = SduiJson.encodeToString(JsonElement.serializer(), body)
            SduiScreenResult.Success(parseScreen(json))
        } catch (e: ClientException) {
            when (e.statusCode) {
                404 -> SduiScreenResult.NotFound
                403 -> SduiScreenResult.Forbidden
                else -> SduiScreenResult.Error("Could not load the screen (error ${e.statusCode}).")
            }
        } catch (e: ServerException) {
            SduiScreenResult.Error("The server is unavailable right now.")
        } catch (e: IOException) {
            SduiScreenResult.Error("Connection failed. Check your network and try again.")
        } catch (e: IllegalStateException) {
            SduiScreenResult.Error("Configuration error while loading the screen.")
        } catch (e: UnsupportedOperationException) {
            SduiScreenResult.Error("Unexpected response from the server.")
        } catch (e: SerializationException) {
            SduiScreenResult.Error("Could not read the screen received.")
        }
    }

    override suspend fun action(actionId: String, requestBody: JsonObject): SduiActionHttpResult =
        actionRepository.invoke(actionId, requestBody)
}
