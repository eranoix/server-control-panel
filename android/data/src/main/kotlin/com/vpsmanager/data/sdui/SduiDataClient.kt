package com.vpsmanager.data.sdui

import com.vpsmanager.mobileapiclient.api.MobileApi
import com.vpsmanager.mobileapiclient.infrastructure.ApiClient
import com.vpsmanager.mobileapiclient.infrastructure.ClientError
import com.vpsmanager.mobileapiclient.infrastructure.ClientException
import com.vpsmanager.mobileapiclient.infrastructure.RequestConfig
import com.vpsmanager.mobileapiclient.infrastructure.RequestMethod
import com.vpsmanager.mobileapiclient.infrastructure.ServerError
import com.vpsmanager.mobileapiclient.infrastructure.ServerException
import com.vpsmanager.mobileapiclient.infrastructure.Success
import kotlinx.serialization.json.JsonElement
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.Call
import java.io.IOException

/**
 * The prefix `ServerConfigRepository.publishLegacyBasePathSeam` appends to the
 * server URL when publishing [ApiClient.BASE_URL_KEY] — that is, the suffix
 * [MobileApi.defaultBasePath] ALWAYS carries in production.
 */
private const val BFF_PATH_PREFIX = "/api/mobile/v1"

/**
 * Returns the server root from a base already prefixed by the BFF.
 *
 * WHY THIS EXISTS (a production bug). [MobileApi.defaultBasePath] resolves, in
 * production, to `https://server/api/mobile/v1` — the base against which every
 * GENERATED method resolves its RELATIVE path (`/screens/{id}`). But
 * [SduiDataClient.call] does not take a relative path: it takes an ABSOLUTE
 * path from the root of the server (`/api/mobile/v1/screens/scheduler.jobs`),
 * whether written by [SduiRepository]/[SduiActionRepository] or arriving from
 * the descriptor's `rows_source`. Adding the two together produced
 * `/api/mobile/v1/api/mobile/v1/screens/scheduler.jobs` — a route the server
 * does not register, answered with a 404, which the data layer translated into
 * `NotFound` and the Admin screen showed as "This section does not exist". The
 * whole SDUI surface (descriptors, table rows and actions) fell over this.
 *
 * The bug did not show up in the tests because they built the client with
 * `server.url("/")` — a base WITHOUT the BFF prefix, which is precisely the one
 * shape in which the concatenation happened to work.
 *
 * It strips the known SUFFIX instead of extracting the URL's "origin" on
 * purpose: that way an installation served under a sub-path
 * (`https://host/vpsm/api/mobile/v1`) stays correct — it becomes `https://host/vpsm`,
 * never `https://host`.
 *
 * While the app has not been configured, [MobileApi.defaultBasePath] is the
 * relative `/api/mobile/v1` itself; stripping the suffix returns an empty
 * string, which [ApiClient] rejects with
 * `IllegalStateException("baseUrl is invalid.")`. That is wanted — it goes on
 * failing loudly, never resolving to `localhost`.
 */
internal fun serverRootOf(basePath: String): String =
    basePath.trimEnd('/').removeSuffix(BFF_PATH_PREFIX)

/**
 * The one client capable of calling an SDUI-descriptor-supplied endpoint
 * verbatim. Every other generated client method (see [MobileApi]) hardcodes
 * its own path at codegen time; an SDUI `rows_source`/`data_source`/
 * `series_source` names its endpoint and method at runtime, so there is no
 * generated method for it. This class adds no new HTTP surface — it reuses
 * [ApiClient]'s existing request/response machinery (same [Call.Factory])
 * already trusted for every other BFF call, just with a path chosen at
 * runtime instead of compile time.
 *
 * The effective base is NOT that of a generated method: since [call] takes an
 * absolute path from the root of the server, [basePath] goes through
 * [serverRootOf] before reaching the [ApiClient]. Without that the BFF prefix
 * lands in the URL twice — see the KDoc of [serverRootOf].
 */
class SduiDataClient(
    basePath: String = MobileApi.defaultBasePath,
    client: Call.Factory = ApiClient.defaultClient,
) : ApiClient(serverRootOf(basePath), client) {

    /**
     * Calls [endpoint] (a full path, e.g. `/api/mobile/v1/docker/containers`,
     * optionally carrying its own `?query=string`) with [method], returning
     * the decoded JSON body. The endpoint's query string, if any, is parsed
     * through okhttp's own [HttpUrl] parser (never hand-rolled) so a value
     * like `/api/mobile/v1/system/history?metric=cpu` reaches the server as
     * a real query parameter instead of a literal `?` inside a path segment.
     *
     * @throws IllegalStateException If the request is not correctly configured
     * @throws IOException Rethrows the OkHttp execute method exception
     * @throws UnsupportedOperationException If the API returns an informational or redirection response
     * @throws ClientException If the API returns a client error response
     * @throws ServerException If the API returns a server error response
     */
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
