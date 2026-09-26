package com.vpsmanager.data.whatsapp

import com.vpsmanager.mobileapiclient.api.WhatsappApi
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/**
 * Derives the absolute `/ws/whatsapp` URL from the same host configuration
 * knob every generated BFF call already reads
 * (`ApiClient.BASE_URL_KEY`/`WhatsappApi.defaultBasePath`), instead of
 * inventing a second one.
 *
 * Today nothing in this app ever sets that system property to an absolute
 * URL -- its only defined value is the relative default `/api/mobile/v1`
 * (`openapi/mobile-v1.yaml`'s `servers.url`), which cannot resolve to a real
 * host: see [WhatsAppRepository]'s own generated `WhatsappApi` calls, which
 * are equally unable to reach a real server until whatever future
 * login/config screen sets an absolute base URL. This function returns null
 * in that (today's actual) case -- callers must not attempt to connect
 * without a resolvable host -- and returns a correct `ws(s)://host/ws/whatsapp`
 * the moment that app-wide gap is closed elsewhere.
 *
 * `/ws/whatsapp` is mounted on the server's root mux, not under
 * `/api/mobile/v1` (`internal/api/api.go`), so only the scheme and
 * host:port survive from [basePath] -- its path component is dropped.
 */
fun resolveWhatsAppWsUrl(basePath: String): String? {
    val httpUrl = basePath.toHttpUrlOrNull() ?: return null
    val hostAndPath = httpUrl.newBuilder()
        .encodedPath("/ws/whatsapp")
        .encodedQuery(null)
        .build()
        .toString()
    val wsScheme = if (httpUrl.isHttps) "wss" else "ws"
    return hostAndPath.replaceFirst(httpUrl.scheme, wsScheme)
}

/**
 * Convenience overload reading [WhatsappApi.defaultBasePath] directly, so
 * `:feature-whatsapp` (which may not reference `:data:mobile-api-client`
 * types -- only `:data` and `:data:mobile-api-client` itself may) can derive
 * its WS URL without importing the generated client.
 */
fun resolveWhatsAppWsUrl(): String? = resolveWhatsAppWsUrl(WhatsappApi.defaultBasePath)
