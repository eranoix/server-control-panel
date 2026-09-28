package dev.servercontrolpanel.data.sdui

import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.RequestMethod
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import java.io.IOException

data class SduiSection(
    val id: String,
    val group: String,
    val label: String,
)

sealed interface SduiSectionsResult {
    data class Success(val sections: List<SduiSection>) : SduiSectionsResult

    data class Error(val reason: String) : SduiSectionsResult
}

fun interface SduiCatalogPort {
    suspend fun sections(): SduiSectionsResult
}

class SduiCatalogRepository(
    private val client: SduiDataClient = SduiDataClient(),
) : SduiCatalogPort {

    override suspend fun sections(): SduiSectionsResult =
        try {
            val body: JsonElement = client.call("/api/mobile/v1/screens", RequestMethod.GET)
                ?: return SduiSectionsResult.Error("Empty response from the server.")
            SduiSectionsResult.Success(parseSections(body))
        } catch (e: ClientException) {
            when (e.statusCode) {
                401 -> SduiSectionsResult.Error("Your session has expired. Sign in again.")
                404 -> SduiSectionsResult.Error("This server does not offer the section list yet.")
                else -> SduiSectionsResult.Error("Could not load the sections (error ${e.statusCode}).")
            }
        } catch (e: ServerException) {
            SduiSectionsResult.Error("The server is unavailable right now.")
        } catch (e: IOException) {
            SduiSectionsResult.Error("Connection failed. Check your network and try again.")
        } catch (e: IllegalStateException) {
            SduiSectionsResult.Error("Configuration error while loading the sections.")
        } catch (e: UnsupportedOperationException) {
            SduiSectionsResult.Error("Unexpected response from the server.")
        } catch (e: SerializationException) {
            SduiSectionsResult.Error("Could not read the section list.")
        }

    private fun parseSections(body: JsonElement): List<SduiSection> {
        val array = body.jsonObject["sections"]?.jsonArray ?: return emptyList()
        return array.mapNotNull { element ->
            val obj = element.jsonObject
            val id = obj["id"].textOrNull() ?: return@mapNotNull null
            val label = obj["label"].textOrNull() ?: return@mapNotNull null
            SduiSection(
                id = id,
                group = obj["group"].textOrNull() ?: "Other",
                label = label,
            )
        }
    }
}

private fun JsonElement?.textOrNull(): String? {
    val primitive = this as? JsonPrimitive ?: return null
    if (primitive is JsonNull) return null
    return primitive.content.takeIf { it.isNotBlank() }
}
