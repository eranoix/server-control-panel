package dev.servercontrolpanel.data.sdui

import dev.servercontrolpanel.mobileapiclient.api.MobileApi
import dev.servercontrolpanel.mobileapiclient.infrastructure.ApiClient
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientError
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.RequestConfig
import dev.servercontrolpanel.mobileapiclient.infrastructure.RequestMethod
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerError
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import dev.servercontrolpanel.mobileapiclient.infrastructure.Success
import kotlinx.serialization.json.JsonElement
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.Call
import java.io.IOException

private const val BFF_PATH_PREFIX = "/api/mobile/v1"

internal fun serverRootOf(basePath: String): String =
    basePath.trimEnd('/').removeSuffix(BFF_PATH_PREFIX)

class SduiDataClient(
    basePath: String = MobileApi.defaultBasePath,
    client: Call.Factory = ApiClient.defaultClient,
) : ApiClient(serverRootOf(basePath), client) {

    @Suppress("UNCHECKED_CAST")
    @Throws(
        IllegalStateException::class,
        IOException::class,
        UnsupportedOperationException::class,
        ClientException::class,
        ServerException::class,
    )
    suspend fun call(endpoint: String, method: RequestMethod, body: JsonElement? = null): JsonElement? {
        val path = endpoint.substringBefore('?')
        val rawQuery = endpoint.substringAfter('?', "")
        val query: MutableMap<String, List<String>> = mutableMapOf()
        if (rawQuery.isNotEmpty()) {
            val parsed = "http://sdui-endpoint.invalid/?$rawQuery".toHttpUrlOrNull()
            parsed?.queryParameterNames?.forEach { name ->
                query[name] = parsed.queryParameterValues(name).filterNotNull()
            }
        }
        val config = RequestConfig<JsonElement?>(
            method = method,
            path = path.trimStart('/'),
            query = query,
            requiresAuthentication = true,
            body = body,
        )
        val response = request<JsonElement?, JsonElement>(config)
        return when (response) {
            is Success<*> -> response.data as JsonElement?
            is ClientError<*> ->
                throw ClientException(
                    "Client error : ${response.statusCode} ${response.message.orEmpty()}",
                    response.statusCode,
                    response,
                )
            is ServerError<*> ->
                throw ServerException(
                    "Server error : ${response.statusCode} ${response.message.orEmpty()} ${response.body}",
                    response.statusCode,
                    response,
                )
            else -> throw UnsupportedOperationException(
                "SDUI data source did not return a success/error response: $endpoint",
            )
        }
    }
}
