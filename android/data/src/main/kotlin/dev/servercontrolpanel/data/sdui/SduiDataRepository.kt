package dev.servercontrolpanel.data.sdui

import dev.servercontrolpanel.core.sdui.SduiDataSource
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.RequestMethod
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import java.io.IOException

sealed interface SduiDataResult {
    data class Success(val body: JsonElement) : SduiDataResult
    data object Empty : SduiDataResult
    data class Error(val reason: String) : SduiDataResult
}

private const val ALLOWED_ENDPOINT_PREFIX = "/api/mobile/v1/"

class SduiDataRepository(
    private val client: SduiDataClient = SduiDataClient(),
) {
    suspend fun fetch(dataSource: SduiDataSource): SduiDataResult {
        val endpoint = dataSource.endpoint
        if (!endpoint.startsWith(ALLOWED_ENDPOINT_PREFIX)) {
            return SduiDataResult.Error("Endpoint outside the mobile BFF namespace: $endpoint")
        }
        val method = when (dataSource.method?.uppercase()) {
            null, "GET" -> RequestMethod.GET
            "POST" -> RequestMethod.POST
            "PUT" -> RequestMethod.PUT
            "PATCH" -> RequestMethod.PATCH
            "DELETE" -> RequestMethod.DELETE
            else -> return SduiDataResult.Error("Unsupported HTTP method: ${dataSource.method}")
        }
        return try {
            val body = client.call(endpoint, method)
            if (body == null || body is JsonNull) {
                SduiDataResult.Empty
            } else {
                SduiDataResult.Success(body)
            }
        } catch (e: ClientException) {
            SduiDataResult.Error("Could not load the data (error ${e.statusCode}).")
        } catch (e: ServerException) {
            SduiDataResult.Error("The server is unavailable right now.")
        } catch (e: IOException) {
            SduiDataResult.Error("Connection failed. Check your network and try again.")
        } catch (e: IllegalStateException) {
            SduiDataResult.Error("Configuration error while loading the data.")
        } catch (e: UnsupportedOperationException) {
            SduiDataResult.Error("Unexpected response from the server.")
        } catch (e: Exception) {
            SduiDataResult.Error("Could not load the data.")
        }
    }
}
