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

private val SECTION_ID_PATTERN = Regex("^[A-Za-z0-9._-]+$")

sealed interface SduiScreenResult {
    data class Success(val envelope: SduiEnvelope) : SduiScreenResult

    data object NotFound : SduiScreenResult

    data object Forbidden : SduiScreenResult

    data class Error(val reason: String) : SduiScreenResult
}

interface SduiScreenPort {
    suspend fun screen(sectionId: String): SduiScreenResult

    suspend fun action(actionId: String, requestBody: JsonObject): SduiActionHttpResult
}

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
