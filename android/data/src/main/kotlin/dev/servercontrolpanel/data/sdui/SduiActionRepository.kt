package dev.servercontrolpanel.data.sdui

import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientError
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.RequestMethod
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import java.io.IOException

private val ACTION_ID_PATTERN = Regex("^[A-Za-z0-9._-]+$")

sealed interface SduiActionHttpResult {
    data class Success(val body: JsonObject) : SduiActionHttpResult

    data class ValidationFailed(val rawBody: String) : SduiActionHttpResult

    data object NotFound : SduiActionHttpResult

    data object Stale : SduiActionHttpResult

    data class Error(val reason: String) : SduiActionHttpResult
}

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
